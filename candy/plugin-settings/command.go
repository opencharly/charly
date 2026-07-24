package settings

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
)

// command.go — the externalized `charly settings` command. The plugin OWNS the get/set/list/reset/path
// subcommand grammar + the output AND the config subsystem itself (config.go, wave γ — ported from
// charly/runtime_config_values.go + charly/host_build_settings.go): read/write
// ~/.config/charly/config.yml is pure sdk/kit file I/O, and the credential-store touches
// (vnc.password.*, secret_backend) dispatch verb:credential directly via InvokeProvider. No core
// round-trip left — the former "settings" HostBuild seam is retired.
//
// settings is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), giving exec its InvokeProvider
// capability. The out-of-process CliMain path has no reverse channel, so the credential-touching ops
// error cleanly (config.go's credentialCall nil-exec guard); get/set/list/reset/path on non-credential
// keys work even there, since they are pure kit.LoadRuntimeConfig/SaveRuntimeConfig file I/O.

const settingsUsage = `usage: charly settings <get <key> | set <key> <value> | list | path | reset [key]>`

// runSettingsCLI dispatches the settings subcommand (the first token) directly against the
// ported config subsystem (config.go), then formats output exactly as the former in-core
// settings subtree did.
func runSettingsCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", settingsUsage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "get":
		if len(rest) != 1 {
			return fmt.Errorf("usage: charly settings get <key>")
		}
		val, err := resolveSettingsGet(ctx, exec, rest[0])
		if err != nil {
			return err
		}
		fmt.Println(val)
	case "set":
		if len(rest) != 2 {
			return fmt.Errorf("usage: charly settings set <key> <value>")
		}
		if err := SetConfigValue(ctx, exec, rest[0], rest[1]); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Set %s = %s\n", rest[0], rest[1])
	case "list":
		vals, err := ListConfigValues()
		if err != nil {
			return err
		}
		for _, v := range vals {
			fmt.Printf("%-15s %-10s (%s)\n", v.Key, v.Value, v.Source)
		}
	case "path":
		path, err := kit.RuntimeConfigPath()
		if err != nil {
			return err
		}
		fmt.Println(path)
	case "reset":
		key := ""
		if len(rest) == 1 {
			key = rest[0]
		} else if len(rest) > 1 {
			return fmt.Errorf("usage: charly settings reset [key]")
		}
		if err := ResetConfigValue(ctx, exec, key); err != nil {
			return err
		}
		if key == "" {
			fmt.Fprintln(os.Stderr, "Reset all config to defaults")
		} else {
			fmt.Fprintf(os.Stderr, "Reset %s to default\n", key)
		}
	default:
		return fmt.Errorf("unknown settings subcommand %q\n%s", sub, settingsUsage)
	}
	return nil
}
