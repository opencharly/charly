package substratekind

// resolve.go — candy/plugin-substrate's OpResolve leg (the substrate-template
// de-type, Cutover I): project an authored local:/android: TEMPLATE into a Resolved*
// envelope the kernel consumes without importing the concrete spec.Local / spec.Android.

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

func resolveSubstrateTemplate(in spec.SubstrateTemplateResolveRequest) ([]byte, error) {
	switch {
	case in.Local != nil:
		var l spec.Local
		if err := json.Unmarshal(in.Local.Local, &l); err != nil {
			return nil, fmt.Errorf("local resolve: decode: %w", err)
		}
		reply := spec.LocalResolveReply{Resolved: &spec.ResolvedLocal{
			Candy:       l.Candy,
			InstallOpts: l.InstallOpts,
			Env:         l.Env,
			Description: l.Description,
			Plan:        l.Plan,
			Raw:         in.Local.Local,
		}}
		return json.Marshal(reply)
	case in.Android != nil:
		var a spec.Android
		if err := json.Unmarshal(in.Android.Android, &a); err != nil {
			return nil, fmt.Errorf("android resolve: decode: %w", err)
		}
		reply := spec.AndroidResolveReply{Resolved: &spec.ResolvedAndroid{
			Serial:        a.Serial,
			Device:        a.Device,
			ApiLevel:      a.ApiLevel,
			GoogleAccount: a.GoogleAccount,
			Plan:          a.Plan,
			Box:           a.Box,
			Adb:           a.Adb,
			Raw:           in.Android.Android,
		}}
		return json.Marshal(reply)
	default:
		return nil, fmt.Errorf("substrate template resolve: neither local nor android set")
	}
}
