package main

// Deploy-target provider signpost. ALL FIVE deploy substrates are now EXTERNAL
// (out-of-process plugins) — there are NO in-proc DeployTargetProviders left;
// ResolveTarget routes every substrate to externalDeployTarget over the E3b reverse
// channel. The core build engines they once wrapped are invoked host-side from each
// substrate's lifecycle hook (vm/pod) or preresolver (android/k8s):

// the `local` deploy substrate is external (candy/plugin-deploy-local).

// the `vm` deploy substrate is external (candy/plugin-deploy-vm); its host-side
// lifecycle hook (vm_deploy_lifecycle.go) boots the domain + builds the guest SSH executor.

// the `pod` deploy substrate is external (candy/plugin-deploy-pod); its host-side
// lifecycle (the plugin, M4) builds the overlay container image via the core prep+resolve
// seam (build_overlay.go, over HostBuild("overlay")) + the candy's own render (deploykit.OCITarget
// + "step-emit"/"oci-emit-step" per-step dispatch, P11c) + owns the
// config/start/remove lifecycle (over HostBuild("cli")).

// android and k8s are EXTERNAL deploy substrates (F1), served out-of-process by
// candy/plugin-adb (deploy:android) / candy/plugin-kube (deploy:k8s). ResolveTarget routes
// `target: android` / `target: k8s` to externalDeployTarget; the host-side substrate
// preresolution (device-endpoint + apk specs; cluster template + Capabilities → the
// generated Kustomize tree) lives in android_deploy_preresolve.go / k8s_deploy_preresolve.go.
