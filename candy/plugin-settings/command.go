package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// command.go — the externalized `charly settings` command. The plugin OWNS the get/set/list/reset/path
// subcommand grammar + the output; the config subsystem (read/write ~/.config/charly/config.yml + the
// credential store + engine/runtime resolution) stays in core and is reached via the generic "settings"
// HostBuild seam. No hidden core-command forward.
//
// settings is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), so HostBuild("settings") reaches
// the host config subsystem. The out-of-process CliMain path has no reverse channel, so it errors.

const settingsUsage = `usage: charly settings <get <key> | set <key> <value> | list | path | reset [key]>`

// runSettingsCLI dispatches the settings subcommand (the first token) and drives the config op over the
// "settings" HostBuild seam, then formats output exactly as the former in-core settings subtree did.
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
		reply, err := hostSettings(ctx, exec, spec.SettingsRequest{Op: "get", Key: rest[0]})
		if err != nil {
			return err
		}
		fmt.Println(reply.Value)
	case "set":
		if len(rest) != 2 {
			return fmt.Errorf("usage: charly settings set <key> <value>")
		}
		if _, err := hostSettings(ctx, exec, spec.SettingsRequest{Op: "set", Key: rest[0], Value: rest[1]}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Set %s = %s\n", rest[0], rest[1])
	case "list":
		reply, err := hostSettings(ctx, exec, spec.SettingsRequest{Op: "list"})
		if err != nil {
			return err
		}
		for _, v := range reply.Entries {
			fmt.Printf("%-15s %-10s (%s)\n", v.Key, v.Value, v.Source)
		}
	case "path":
		reply, err := hostSettings(ctx, exec, spec.SettingsRequest{Op: "path"})
		if err != nil {
			return err
		}
		fmt.Println(reply.Value)
	case "reset":
		key := ""
		if len(rest) == 1 {
			key = rest[0]
		} else if len(rest) > 1 {
			return fmt.Errorf("usage: charly settings reset [key]")
		}
		if _, err := hostSettings(ctx, exec, spec.SettingsRequest{Op: "reset", Key: key}); err != nil {
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

// hostSettings runs one config-subsystem op over the generic "settings" HostBuild kind. exec is nil on
// the out-of-process cliMain path (no reverse channel) → a clear error.
func hostSettings(ctx context.Context, exec *sdk.Executor, req spec.SettingsRequest) (spec.SettingsReply, error) {
	if exec == nil {
		return spec.SettingsReply{}, fmt.Errorf("charly settings requires compiled-in placement (the settings host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.SettingsReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "settings", reqJSON)
	if err != nil {
		return spec.SettingsReply{}, err
	}
	var reply spec.SettingsReply
	if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
		return spec.SettingsReply{}, uerr
	}
	if reply.Error != "" {
		return spec.SettingsReply{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}
