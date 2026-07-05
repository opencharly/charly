# OpenCharly — The Vision

*The thesis behind the candybox.*

Most other tools hand an agent a sandbox — and then spend their effort taking things *away*: fewer commands, no network, no installs, safety bought by stripping the candy out, and most of the capability lost with it. Charly does the opposite. It treats the agent to a full candy store — a whole factory ready to produce every candy imaginable.

## The core purpose

Every candy, every box, every probe serves one end: **to build a better candybox — because a better candybox is how charly builds a better charly.** The whole factory exists to forge the tools, recipes, and proving grounds it needs to out-build its current self, then turn them on itself and do exactly that. It is the chocolatier who perfects their own kitchen so the next kitchen comes out finer still — a flywheel where every turn of the crank makes the next turn easier, and each candybox is the means to a better one. The eleven tenets below are *how* that flywheel is built to spin safely and fearlessly; tenet 11 is the flywheel made literal — the factory nested in its own box, rebuilding itself. This is what all that candy is *for*.

## The tenets

1. **Secure the room, not the candy.** Safety lives at the boundary of a candybox — rootless containers, isolated VMs, encrypted volumes — never in a shrunken toolset. A walled room you can hand over *completely* beats an empty sandbox you keep narrowing.
   → CLAUDE.md "Candyboxing".

2. **One recipe, many boxes.** A single declarative recipe — candies stacked into a box — pours into every mold: an interactive shell, a managed pod, a host workstation, a k8s cluster, a bootable VM, an Android device. Write the recipe once; let `charly` set it in whatever shape the moment needs.
   → README "Build → run → deploy → evaluate".

3. **Every candy ships with its recipe card.** Every candy, box, and verb carries a dedicated skill, so nothing in the candy store is a mystery — neither you nor your agents ever have to guess what a piece does, how it's made, or how it should taste.
   → `plugins/README.md`.

4. **Two tasters at one bench.** The same `charly` surface serves you at the keyboard and your agents driving the line, with no second-class channel for either. Built for you *and* your agents, in the same breath.
   → CLAUDE.md "Candyboxing", `/charly-internals:agents`.

5. **Taste every candy before making the recipe — Risk Driven Development.** Recipe cards drift and vats spring leaks, so nothing high-stakes should ride on "I think that's how everyone does it". The riskiest question — *do these candies actually melt together the way I think they do* — gets proven on a real, disposable candybox first — a **spike**: a small, time-boxed tasting batch, thrown away once it has answered the question; only the knowledge is kept, never the batch. Reality is the only ground truth. Risk Driven Development decides *what* to prove, and *when*: the riskiest unknown, first — and the spike is how it proves it.
   → CLAUDE.md "Risk Driven Development (RDD)", `/charly-check:check`.

6. **Write down what "good" means, and have an agent taste it — Agent Driven Evaluation.** What a candybox should be is captured as a runnable acceptance plan, not left to opinion. The plan's deterministic `check:` steps verify the measurable; for the subtle "is it actually right?" an `agent-check:` step has an agent taste the live batch with the full probe kit and judge. Agent Driven Evaluation is Risk Driven Development's co-equal twin: RDD proves the risky assumptions a behavior rests on; ADE pins down what correct behavior *is* and grades the live batch against it.
   → CLAUDE.md "Agent Driven Evaluation (ADE)", `/charly-check:check`.

7. **Carve the master pattern before building the machine — Schema Driven Design.** The shape of everything the factory accepts — every candy, box, and mold setting an author can write down — is carved ONCE, as a master pattern, before any machinery is built to handle it. Everything that needs the shape is cast from that single carving: the parts the machines work with, the gauge at the factory door that checks each authored recipe, the refit that modernizes old ones. Nothing is copied freehand beside the pattern — a freehand copy drifts; a casting cannot. When a new shape is needed, the pattern is carved (and spiked) first; the machinery follows from it.
   → CLAUDE.md "Schema Driven Design (SDD)", `/charly-internals:go`, `/charly-internals:plugin`.

8. **Conched smooth, then tempered to set.** Running a continuous check loop down to the micrometer proves a candybox works; running the build → deploy → prove → iterate mantra *over and over again* makes it good. Like conching chocolate, every pass grinds the candy smoother — deduplicated, fully refined, dead code and band-aid fixes removed, yet another round of checks added, re-proven on a live deployment — until the candybox has proven itself and the candy is silk; the first box released for public consumption should taste like the finest milk chocolate ever, not like a rock sprayed with quick-drying brown paint. But smooth is not the same as *set*. After conching comes tempering — the patient warming and cooling that coaxes the cocoa butter to crystallize the right way, so the chocolate sets into one stable crystal instead of a jumble of unstable ones: glossy, a clean snap, melting evenly, and keeping its shine on the shelf rather than blooming dull and streaky. Seed the right crystal and it holds; get it wrong and it blooms. A candybox is tempered the same way — brought to one correct, reproducible state and held there: its version read from its own contents, so it stays put whenever nothing changed; its candies nucleating around one matching set of versions instead of a half-pinned jumble; the whole thing re-proven from a clean rebuild — built, deployed, brought to a healthy running state, graded against its own baked-in checklist of what "good" means — every time, not once. The bloom we engineer out is the box that builds today and drifts tomorrow, or runs once and misbehaves on the rebuild: a set that never truly held.
   → CLAUDE.md "Prioritize Clean Architecture Above All Else", R10, README "Build", `/charly-build:build`, `/charly-internals:strict-policy`, `/charly-internals:capabilities`, `/charly-build:reconcile`, `/charly-check:check`.

9. **Every spoiled batch is a new lesson waiting to be learned.** Every candybox is both a testbed and the recipe for the final product by explicit design, so a failed batch costs nothing but the lesson inside it: melt it down, learn what went wrong, and pour the next one wiser. A failure is feedback to be mined, never an incident to be prevented at all costs — and that is exactly what lets autonomous iteration be *fearless* and *safe* at once. Disposability is the license to be bold.
   → CLAUDE.md "Disposable-Only Autonomy", `/charly-internals:disposable`.

10. **Free to forge a better candybox.** And when the box itself is wrong — the wrong mix of candies, a missing one, a composition that won't melt together — the agent forges a fresh box rather than make do with a broken design. Because a candybox is just a recipe, and a throwaway one, building the right box from scratch costs no more than patching the wrong one — so a clean rebuild always beats a workaround. The freedom to make every candybox perfect is what keeps the whole candy store a pleasure to work in.
   → CLAUDE.md "Candyboxing", "Disposable-Only Autonomy", `/charly-internals:disposable`.

11. **The factory fits in a box, too — candyboxes all the way down.** One of the molds a recipe can pour into is the *factory itself*: the whole `charly` line, nested inside one of its own disposable candyboxes. The forging happens *one level in* — from inside that outer box the factory builds, deploys, and tastes *fresh* candyboxes on live deployments and melts the spoiled batches back down. A candybox forged inside a candybox: that nesting is the whole trick. And it runs as a loop — the entire pass turns inside the box, the evaluation verdict deciding the next one. That is *factory-in-the-loop evaluation*: the production line being part of its own feedback loop, tasting in the driver's seat. Because the outer box is as throwaway as the boxes it builds, the line proves and rebuilds itself fearlessly — a candybox that builds candyboxes is how verification becomes self-hosting.
   → CLAUDE.md "Candyboxing", "Disposable-Only Autonomy", `/charly-check:check` (the `kind: check` beds + the `iterate:` loop), `/charly-internals:disposable`.

## Where the factory is heading

- **Widen what one recipe can become.** The same declaration already pours into containers, VMs, k8s, hosts, and Android — the long arc is *more molds under one wrapper*, never more wrappers to learn.
- **A tiny kitchen, every candy a recipe.** The factory floor stays small on purpose — a kernel that loads, composes, gates, builds, and dispatches, and nothing else. Every capability the factory has — every verb, mold, probe, builder, and command — is a candy speaking one shared SDK, so the candy store grows without the kitchen growing: a new capability is a new recipe, never a bigger kitchen, and "it needs the kitchen" means a new generic counter the recipe reaches over, never a new fixture bolted to the floor.
  → CLAUDE.md "Core is the kernel; EVERY capability is a plugin candy".
- **Hand the whole line to the agents.** The full loop, run end-to-end *inside* the candybox — agents free to forge a fresh, better box whenever the job needs one — with you watching the floor rather than turning every crank.
- **Verification becomes the cadence, not a checkpoint.** The long arc of *prove it first* (Risk Driven Development) and *the spec is the test* (Agent Driven Evaluation) is a single loop: the agent writes down what a good candy is as a runnable acceptance plan, proves the riskiest unknowns on a live, disposable candybox *before* it commits to a recipe, and grades its own acceptance against the running box — until *never trust, verify* is the factory's default rhythm, woven through every batch, not a discipline anyone has to remember to apply.
  → CLAUDE.md "Risk Driven Development (RDD)" + "Agent Driven Evaluation (ADE)".
- **A shared candy store.** Cross-repo, versioned candies and boxes (`@github` refs, content-derived versions) maturing into an ecosystem you *compose from*, not a pantry you restock by hand.
- **The long bet.** As agents grow more capable, the winning environment is a fully-stocked candy store, not a tighter sandbox. Charly is built for that world — and built to still feel like a magic chocolate factory when it arrives.

*And we are looking forward to the day every agent asks for a blowtorch — not to wave it around or hurt anyone, but to caramelize the top of a perfect crème brûlée.*

---

*The factory floor is documented in [README.md](README.md); the house rules in [CLAUDE.md](CLAUDE.md); every candy and box has a recipe card in [plugins/README.md](plugins/README.md). Dated history lives — and only lives — in each repo's [CHANGELOG/](CHANGELOG/README.md), one file per CalVer version.*
