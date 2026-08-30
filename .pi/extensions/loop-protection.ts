/**
 * loop-protection.ts — repetition-loop protection for the pi agent.
 *
 * RCA (2026-08-30, from live session evidence): a model in a degenerate
 * state can emit the SAME tool call dozens/hundreds of times in one response
 * (observed: 317 identical fabric_exec calls, 43K tokens) and repeat text
 * tokens ("torso" x 2000+). The response then hits the provider's TOTAL
 * output cap (max_tokens is thinking + answer; with xhigh thinking the
 * answer room is small), the harness fails every call with a context-less
 * "Re-issue the tool call with complete arguments" error, and the model
 * repeats — a self-amplifying loop that burned 600M+ tokens.
 *
 * This extension protects at the tool layer (per-repo, works with any
 * thinking level):
 *
 *  1. tool_call — blocks the Nth consecutive identical tool call (dedup)
 *     and blocks re-issues of calls from a truncated response, replacing
 *     the harness's "re-issue" instruction with a corrective directive.
 *  2. message_end — detects truncated responses (stopReason "length"),
 *     identical-call repetition, and degenerate text repetition
 *     ("torso" x N); tracks consecutive truncations.
 *  3. context — when a loop is detected (>= 2 consecutive truncations or
 *     degeneration), injects a corrective message before the next LLM
 *     call so the model changes behavior instead of repeating.
 *  4. tool_result — auto-compaction: bounds huge tool results so the
 *     context cannot flood (the flood is what drives the model into the
 *     degenerate state).
 *
 * The upstream core fix (mid-stream repetition abort + truncation-path
 * auto-compaction) lives in the pi repo; this extension is the per-repo
 * safety net that works today.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

/** Consecutive identical calls beyond this count are a repetition loop. */
const REPEAT_BLOCK_THRESHOLD = 3;
/** Consecutive truncated responses beyond this count trigger a corrective injection. */
const TRUNCATION_LOOP_THRESHOLD = 2;
/** A word repeated this many times consecutively in text is degenerate repetition. */
const TEXT_REPETITION_LIMIT = 20;
/** Tool results with more text than this get auto-compacted. */
const MAX_RESULT_CHARS = 20000;
/** Keep this many chars when compacting a tool result. */
const KEEP_RESULT_CHARS = 8000;

/** Strip volatile substrings so near-identical calls count as repeats. */
function normalizeArgs(s: string): string {
	return s
		.replace(/\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?/g, "TS")
		.replace(/[0-9a-f]{8,}/gi, "HEX")
		.replace(/\/tmp\/[^\s"'\`]+/g, "/tmp/PATH")
		.replace(/\d{4,}/g, "N");
}

function hasTextRepetition(text: string): boolean {
	return new RegExp(`(\\w{3,})\\1{${TEXT_REPETITION_LIMIT},}`).test(text);
}

export default function (pi: ExtensionAPI) {
	// Session-scoped state (reset on session_start).
	let truncatedCalls: { toolName: string; args: string }[] = [];
	let lastCallKey: string | null = null;
	let lastCallCount = 0;
	let consecutiveTruncations = 0;
	let degenerationDetected = false;
	let correctiveInjected = false;

	pi.on("session_start", async () => {
		truncatedCalls = [];
		lastCallKey = null;
		lastCallCount = 0;
		consecutiveTruncations = 0;
		degenerationDetected = false;
		correctiveInjected = false;
	});

	pi.on("message_end", async (event) => {
		const msg = event.message as any;
		if (!msg || msg.role !== "assistant") return;
		const content = msg.content || [];
		const calls = content.filter((b: any) => b.type === "toolCall");

		if (msg.stopReason === "length") {
			// Truncated response: record the calls for retry interception.
			truncatedCalls = calls.map((b: any) => ({ toolName: b.name, args: JSON.stringify(b.arguments) }));
			consecutiveTruncations++;

			// Degenerate text repetition ("torso" x N).
			const text = content
				.filter((b: any) => b.type === "text")
				.map((b: any) => b.text)
				.join("");
			if (hasTextRepetition(text)) degenerationDetected = true;

			// Identical-call repetition inside the truncated message.
			const groups = new Map<string, number>();
			for (const c of calls) {
				const key = c.name + "\u0000" + normalizeArgs(JSON.stringify(c.arguments));
				groups.set(key, (groups.get(key) || 0) + 1);
			}
			for (const n of groups.values()) {
				if (n > REPEAT_BLOCK_THRESHOLD) degenerationDetected = true;
			}
		} else if (msg.stopReason === "toolUse" || msg.stopReason === "stop") {
			// Healthy turn: reset the loop state unless the message itself repeats.
			const groups = new Map<string, number>();
			for (const c of calls) {
				const key = c.name + "\u0000" + normalizeArgs(JSON.stringify(c.arguments));
				groups.set(key, (groups.get(key) || 0) + 1);
			}
			const maxRepeat = Math.max(0, ...groups.values());
			if (maxRepeat <= REPEAT_BLOCK_THRESHOLD) {
				consecutiveTruncations = 0;
				degenerationDetected = false;
				correctiveInjected = false;
			}
		}
	});

	pi.on("tool_call", async (event) => {
		const args = JSON.stringify(event.input);

		// 1. Truncation retry: this call was part of a truncated response.
		if (truncatedCalls.length > 0) {
			const hit = truncatedCalls.find(
				(tc) => tc.toolName === event.toolName && normalizeArgs(tc.args) === normalizeArgs(args),
			);
			if (hit) {
				truncatedCalls = [];
				return {
					block: true,
					reason:
						"This tool call was part of a response truncated by the output token limit. " +
						"Do NOT re-issue it — the same call will be truncated again. " +
						"Respond with a SHORT tool call with small arguments, or delegate the exploration to a subagent.",
				};
			}
		}

		// 2. Consecutive identical calls (dedup).
		const key = event.toolName + "\u0000" + normalizeArgs(args);
		if (key === lastCallKey) {
			lastCallCount++;
			if (lastCallCount >= REPEAT_BLOCK_THRESHOLD) {
				return {
					block: true,
					reason:
						`This tool call (${event.toolName}) has been issued ${lastCallCount} times in a row with identical arguments. ` +
						"You are in a repetition loop. STOP repeating the same call — make ONE short tool call " +
						"with small arguments, or delegate the exploration to a subagent.",
				};
			}
		} else {
			lastCallKey = key;
			lastCallCount = 1;
		}
	});

	pi.on("context", async (event) => {
		if (correctiveInjected) return;
		if (consecutiveTruncations < TRUNCATION_LOOP_THRESHOLD && !degenerationDetected) return;
		correctiveInjected = true;
		return {
			messages: [
				...event.messages,
				{
					role: "custom",
					customType: "loop-protection",
					content:
						`[loop-protection] Your last ${consecutiveTruncations} response(s) were truncated by the output token limit ` +
						(degenerationDetected
							? "and showed degenerate repetition (repeated words or identical tool calls). "
							: "because of repeated tool calls. ") +
						"This is a repetition loop. STOP repeating. Make ONE short tool call with small arguments, " +
						"or delegate the exploration to a subagent that returns a concise verdict.",
					display: true,
					timestamp: Date.now(),
				},
			],
		};
	});

	pi.on("tool_result", async (event) => {
		// Auto-compaction: bound huge tool results so the context cannot flood.
		const blocks = (event.content || []) as any[];
		let totalText = 0;
		for (const b of blocks) if (b.type === "text") totalText += b.text.length;
		if (totalText <= MAX_RESULT_CHARS) return;

		const keptBlocks: any[] = [];
		let remaining = KEEP_RESULT_CHARS;
		let omitted = 0;
		for (const b of blocks) {
			if (b.type === "text") {
				if (remaining > 0) {
					const t = b.text.slice(0, remaining);
					keptBlocks.push({ ...b, text: t });
					remaining -= t.length;
					omitted += b.text.length - t.length;
				} else {
					omitted += b.text.length;
				}
			} else {
				keptBlocks.push(b);
			}
		}
		if (omitted > 0) {
			keptBlocks.push({
				type: "text",
				text: `\n…[loop-protection: ${omitted} chars omitted from this tool result to keep the context bounded]`,
			});
			return { content: keptBlocks };
		}
	});
}
