package main

// Deploy-target provider signpost. ALL FIVE deploy substrates are now EXTERNAL
// (out-of-process plugins) — there are NO in-proc DeployTargetProviders left;
// ResolveTarget routes every substrate to pluginDeployTarget (S3b), the thin data-only
// proxy dispatching to candy/plugin-bundle's Invoke(OpDeployDispatch), which reaches the
// substrate provider itself via its own sdk.Executor.InvokeProvider. The remaining core
// build-engine coupling is invoked host-side ONLY from pod's lifecycle (via
// HostBuild("overlay")) or the android/k8s preresolvers — vm's venue lifecycle is
// entirely plugin-side and calls no core build engine:

// the `local` deploy substrate is external (candy/plugin-deploy-local).

// the `vm` deploy substrate is external (candy/plugin-deploy-vm); its ENTIRE venue
// lifecycle (candy/plugin-deploy-vm/lifecycle.go, reached the same generic
// OpDeployDispatch way as every other substrate) boots the domain + builds the
// guest SSH executor — no core-side lifecycle hook remains (S3b).

// the `pod` deploy substrate is external (candy/plugin-deploy-pod); its host-side
// lifecycle (the plugin, M4) builds the overlay container image via the core prep+resolve
// seam (build_overlay.go, over HostBuild("overlay")) + the candy's own render (deploykit.OCITarget
// + "step-emit"/"oci-emit-step" per-step dispatch, P11c) + owns the
// config/start/remove lifecycle (over HostBuild("cli")).

// android and k8s are EXTERNAL deploy substrates (F1), served out-of-process by
// candy/plugin-adb (deploy:android) / candy/plugin-kube (deploy:k8s). ResolveTarget routes
// `target: android` / `target: k8s` to pluginDeployTarget; the substrate preresolution
// (device-endpoint + apk specs; cluster template + Capabilities → the generated Kustomize
// tree) is driven PLUGIN-SIDE (F6, FINAL/K5 unit 6a — each plugin's own preresolve.go,
// dispatched directly by candy/plugin-bundle via sdk.Executor.InvokeProvider(OpPreresolve) —
// S3b dissolved the former core-side deploy_preresolve.go registry, since the caller is now
// itself a plugin and no longer needs a host-side registry indirection), reaching the host
// ONLY through the "deploy-entity-resolve" HostBuild seam for the LoadUnified-coupled config it
// cannot resolve itself. The Kustomize tree GENERATION + WRITE + egress-VALIDATE (formerly the
// "k8s-generate-kustomize" HostBuild seam) is fully plugin-side now (K5-A item 6,
// candy/plugin-kube/materialize.go) — no host round trip.
