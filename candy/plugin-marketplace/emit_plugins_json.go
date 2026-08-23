package marketplace

import (
	"encoding/json"
	"fmt"
	"strings"
)

// emit_plugins_json.go — the per-family marketplace manifests: .claude-plugin/plugin.json
// (Claude Code) + .codex-plugin/plugin.json (Codex) + .mcp.json (MCP servers, only when the
// family declares mcp_servers). Every field derives from the family's #MarketplaceFamily
// declaration — the marketplace entity is the single source for the whole generated plugins/
// surface (a description/keyword edit regenerates the manifests).

const (
	pluginAuthor          = "opencharly"
	pluginRepository      = "https://github.com/opencharly/marketplace"
	marketplaceRepository = "opencharly/marketplace"
	pluginLicense         = "MIT"
)

// claudePluginJSON is the .claude-plugin/plugin.json shape.
// claudePluginJSON is the .claude-plugin/plugin.json shape. There is deliberately NO version
// field: per the Claude Code marketplace docs, an omitted version resolves to the plugin
// source's commit SHA, so users receive updates whenever the marketplace commit changes — the
// continuous-corpus model this org runs on. A pinned version would freeze every installed
// plugin at an explicit bump.
type claudePluginJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
	Homepage   string   `json:"homepage,omitempty"`
	Repository string   `json:"repository"`
	License    string   `json:"license,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	MCPServers string   `json:"mcpServers,omitempty"`
}

// codexPluginJSON is the .codex-plugin/plugin.json shape (the expanded Codex schema).
// codexPluginJSON is the .codex-plugin/plugin.json shape (the expanded Codex schema). Version is
// omitted for the same commit-SHA-versioning reason as the Claude manifest.
type codexPluginJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
	Repository string         `json:"repository"`
	Skills     string         `json:"skills"`
	Interface  codexInterface `json:"interface"`
	MCPServers string         `json:"mcpServers,omitempty"`
}

type codexInterface struct {
	DisplayName      string   `json:"displayName"`
	ShortDescription string   `json:"shortDescription"`
	LongDescription  string   `json:"longDescription"`
	DeveloperName    string   `json:"developerName"`
	Category         string   `json:"category"`
	Capabilities     []string `json:"capabilities"`
	DefaultPrompt    []string `json:"defaultPrompt"`
}

func emitPluginsJSON(em emissions, families []family) {
	for _, f := range families {
		emitClaudePluginJSON(em, f)
		emitCodexPluginJSON(em, f)
		emitMCPJSON(em, f)
	}
}

func emitClaudePluginJSON(em emissions, f family) {
	doc := claudePluginJSON{
		Name:        "charly-" + f.Name,
		Description: f.Meta.Description,
		Author: struct {
			Name string `json:"name"`
		}{Name: pluginAuthor},
		Homepage:   "https://github.com/opencharly/charly",
		Repository: pluginRepository,
		License:    pluginLicense,
		Keywords:   f.Meta.Keywords,
	}
	if len(f.Meta.McpServers) > 0 {
		doc.MCPServers = "./.mcp.json"
	}
	em["plugins/"+f.Name+"/.claude-plugin/plugin.json"] = mustJSON(doc)
}

func emitCodexPluginJSON(em emissions, f family) {
	display := "OpenCharly " + strings.ToUpper(f.Name[:1]) + f.Name[1:]
	doc := codexPluginJSON{
		Name:        "charly-" + f.Name,
		Description: f.Meta.Description,
		Author: struct {
			Name string `json:"name"`
		}{Name: pluginAuthor},
		Repository: pluginRepository,
		Skills:     "./skills/",
		Interface: codexInterface{
			DisplayName:      display,
			ShortDescription: f.Meta.Description,
			LongDescription:  f.Meta.Description,
			DeveloperName:    pluginAuthor,
			Category:         firstNonEmpty(f.Meta.Category, "images"),
			Capabilities:     []string{"Instructions"},
			DefaultPrompt:    []string{"Use " + display + " for this OpenCharly task."},
		},
	}
	if len(f.Meta.McpServers) > 0 {
		doc.MCPServers = "./.mcp.json"
	}
	em["plugins/"+f.Name+"/.codex-plugin/plugin.json"] = mustJSON(doc)
}

// emitMCPJSON emits plugins/<family>/.mcp.json when the family declares mcp_servers (the
// Claude Code MCP server config: {"mcpServers": {"<name>": {...}}}).
func emitMCPJSON(em emissions, f family) {
	if len(f.Meta.McpServers) == 0 {
		return
	}
	servers := make(map[string]map[string]any, len(f.Meta.McpServers))
	for _, s := range f.Meta.McpServers {
		entry := map[string]any{}
		if s.Type == "" || s.Type == "http" {
			entry["type"] = "http"
			entry["url"] = s.Url
		} else {
			entry["type"] = "stdio"
			entry["command"] = s.Command
			if len(s.Args) > 0 {
				entry["args"] = s.Args
			}
		}
		servers[s.Name] = entry
	}
	em["plugins/"+f.Name+"/.mcp.json"] = mustJSON(map[string]any{"mcpServers": servers})
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("marketplace: marshal json: %v", err))
	}
	return append(b, '\n')
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
