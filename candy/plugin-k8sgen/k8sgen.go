// Package k8sgen is the Kustomize-GENERATOR plugin (C8/M13): the pure manifest
// generation that turns (deployment node, kind:k8s cluster template, image
// ports/uid/gid) into a Kustomize base/ + overlays/<inst>/ tree. It was carved out
// of charly core (charly/k8s_generate.go) and is fronted by a thin in-core shim
// (GenerateK8sKustomize) that extracts Ports/UID/GID from the image Capabilities,
// Invokes this plugin's OpEmit, then does the disk I/O + the host-side egress gate
// (ValidateEgressValue) before the bytes hit disk — mirroring the M16 egress
// pattern. Compiled-in (the deploy/from-box paths call it; an in-proc inprocProvider
// pays only a JSON envelope over generation that dominates).
//
// GenerateTree is PURE: no disk I/O, no egress — it collects manifest docs (as JSON)
// at RELATIVE paths and returns them; the host shim owns RemoveAll/MkdirAll, the raw
// manifest copy, the egress validation, and the YAML writes.
package k8sgen

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// -----------------------------------------------------------------------------
// Kustomize manifest generator — Part F core. Collects base/ + overlays/<inst>/
// manifests as RELATIVE-pathed docs (the host shim materializes them on disk).
//
// Scope of this implementation:
//   - Deployment / StatefulSet / DaemonSet / Job / CronJob by kind heuristic
//   - Service (ClusterIP) from capabilities.Ports
//   - PersistentVolumeClaim from deployment.Storage (+ volumeClaimTemplates
//     for StatefulSet)
//   - Ingress (when deployment.Expose.Host is set)
//   - kustomization.yaml wiring
//
// Out of scope — NOT emitted by this generator (a deployment needing any of these
// authors it as an extra overlay manifest alongside the generated base):
//   - ConfigMap / Secret / ExternalSecret
//   - HorizontalPodAutoscaler / PodDisruptionBudget
//   - NetworkPolicy
//   - ServiceMonitor
//   - Gateway API HTTPRoute variant
// -----------------------------------------------------------------------------

// GenerateTree materializes the Kustomize tree as a set of RELATIVE-pathed manifest
// docs (no disk I/O, no egress — the host shim owns both). Returns the relative
// overlay path the host joins onto OutputDir/DeploymentName to form the
// `kubectl apply -k` argument.
//
//nolint:gocyclo // Kustomize orchestrator: sequential phases with conditional file emissions; moderate complexity, extraction needs extensive output-path param passing
func GenerateTree(in spec.K8sGenInput) (spec.K8sGenReply, error) {
	var reply spec.K8sGenReply
	if in.DeploymentName == "" {
		return reply, fmt.Errorf("deployment name is required")
	}
	// NOTE: the Cluster-required guard lives in the in-core shim
	// (GenerateK8sKustomize), where Cluster is still a *K8sSpec pointer that CAN be
	// nil-checked. The wire type here is a value (spec.K8s), so by the time
	// GenerateTree runs the cluster is always present.

	overlayName := in.Instance
	if overlayName == "" {
		overlayName = "default"
	}

	var files []spec.K8sGenFile
	addFile := func(rel string, doc any, kind string) error {
		raw, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("marshaling %s: %w", rel, err)
		}
		files = append(files, spec.K8sGenFile{RelPath: rel, Doc: raw, EgressKind: kind})
		return nil
	}

	// ---- Emit base resources ----
	baseResources := []string{}

	workload, workloadKind := generateWorkload(in)
	if err := addFile("base/"+strings.ToLower(workloadKind)+".yaml", workload, "k8s_object"); err != nil {
		return reply, err
	}
	baseResources = append(baseResources, strings.ToLower(workloadKind)+".yaml")

	if svc := generateService(in, workloadKind); svc != nil {
		if err := addFile("base/service.yaml", svc, "k8s_object"); err != nil {
			return reply, err
		}
		baseResources = append(baseResources, "service.yaml")
	}

	if workloadKind != "StatefulSet" {
		for _, pvc := range generatePVCs(in) {
			name, _ := pvc["metadata"].(map[string]any)["name"].(string)
			if name == "" {
				name = "pvc"
			}
			file := "pvc-" + name + ".yaml"
			if err := addFile("base/"+file, pvc, "k8s_object"); err != nil {
				return reply, err
			}
			baseResources = append(baseResources, file)
		}
	}

	if ing := generateIngress(in); ing != nil {
		if err := addFile("base/ingress.yaml", ing, "k8s_object"); err != nil {
			return reply, err
		}
		baseResources = append(baseResources, "ingress.yaml")
	}

	// Raw manifests from deployment.kubernetes.raw are copied verbatim by the host
	// shim (it owns disk I/O); GenerateTree only registers their kustomize resource
	// paths so the base kustomization.yaml references them.
	if in.Deploy.Kubernetes != nil {
		for _, src := range in.Deploy.Kubernetes.Raw {
			baseResources = append(baseResources, filepath.Join("raw", filepath.Base(src)))
		}
	}

	// ---- Emit base/kustomization.yaml ----
	sort.Strings(baseResources)
	baseKustomization := map[string]any{
		"apiVersion": "kustomize.config.k8s.io/v1beta1",
		"kind":       "Kustomization",
		"resources":  baseResources,
	}
	if labels := mergedLabels(in); len(labels) > 0 {
		// kustomize deprecated `commonLabels` ("Please use 'labels' instead") and warns on
		// every apply. `labels` with includeSelectors:true is its exact replacement — it
		// adds the pairs AND writes them into the selectors, which is what commonLabels did.
		// Plain `labels` without includeSelectors would silently stop labelling selectors.
		baseKustomization["labels"] = []map[string]any{{
			"pairs":            labels,
			"includeSelectors": true,
		}}
	}
	if annotations := in.Cluster.Defaults.Annotations; len(annotations) > 0 {
		baseKustomization["commonAnnotations"] = annotations
	}
	if err := addFile("base/kustomization.yaml", baseKustomization, "kustomization"); err != nil {
		return reply, err
	}

	// ---- Emit overlay kustomization.yaml ----
	overlayKustomization := map[string]any{
		"apiVersion": "kustomize.config.k8s.io/v1beta1",
		"kind":       "Kustomization",
		"resources":  []string{"../../base"},
	}
	ns := deployNamespace(in)
	if ns != "" {
		overlayKustomization["namespace"] = ns
	}
	// Translate deployment.kubernetes.patches into the kustomize "patches" list.
	if in.Deploy.Kubernetes != nil && len(in.Deploy.Kubernetes.Patches) > 0 {
		var patches []map[string]any
		for _, p := range in.Deploy.Kubernetes.Patches {
			entry := map[string]any{
				"patch": p.Patch,
			}
			if p.Target.Kind != "" || p.Target.Name != "" {
				tgt := map[string]any{}
				if p.Target.Kind != "" {
					tgt["kind"] = p.Target.Kind
				}
				if p.Target.Name != "" {
					tgt["name"] = p.Target.Name
				}
				if p.Target.Namespace != "" {
					tgt["namespace"] = p.Target.Namespace
				}
				entry["target"] = tgt
			}
			patches = append(patches, entry)
		}
		overlayKustomization["patches"] = patches
	}
	if err := addFile("overlays/"+overlayName+"/kustomization.yaml", overlayKustomization, "kustomization"); err != nil {
		return reply, err
	}

	reply.OverlayRelPath = "overlays/" + overlayName
	reply.Files = files
	return reply, nil
}

// -----------------------------------------------------------------------------
// Workload kind selection — the heuristic from F.7.
// -----------------------------------------------------------------------------

func selectWorkloadKind(opts spec.K8sGenInput) string {
	// Explicit override wins.
	if k8s := opts.Deploy.Kubernetes; k8s != nil && k8s.Workload != "" {
		return k8s.Workload
	}
	kind := opts.Deploy.Kind
	if kind == "" {
		kind = "service"
	}
	switch kind {
	case "daemon":
		return "DaemonSet"
	case "batch":
		return "Job"
	case "scheduled":
		return "CronJob"
	case "oneshot":
		return "Pod"
	case "service":
		if len(opts.Deploy.Storage) > 0 {
			return "StatefulSet"
		}
		return "Deployment"
	}
	return "Deployment"
}

// -----------------------------------------------------------------------------
// Workload (Deployment / StatefulSet / DaemonSet / Job / CronJob / Pod).
// -----------------------------------------------------------------------------

func generateWorkload(opts spec.K8sGenInput) (map[string]any, string) {
	kind := selectWorkloadKind(opts)
	podSpec := generatePodSpec(opts)

	switch kind {
	case "CronJob":
		spec := map[string]any{
			"schedule": opts.Deploy.Schedule,
			"jobTemplate": map[string]any{
				"spec": map[string]any{
					"template": map[string]any{
						"spec": podSpec,
					},
				},
			},
		}
		return baseManifest("batch/v1", "CronJob", opts, spec), kind
	case "Job":
		spec := map[string]any{
			"template": map[string]any{
				"spec": podSpec,
			},
		}
		return baseManifest("batch/v1", "Job", opts, spec), kind
	case "Pod":
		return baseManifest("v1", "Pod", opts, podSpec), kind
	case "DaemonSet":
		spec := map[string]any{
			"selector": map[string]any{
				"matchLabels": appSelector(opts),
			},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": appSelector(opts),
				},
				"spec": podSpec,
			},
		}
		return baseManifest("apps/v1", "DaemonSet", opts, spec), kind
	case "StatefulSet":
		spec := map[string]any{
			"serviceName": opts.DeploymentName + "-headless",
			"replicas":    effectiveReplicas(opts),
			"selector": map[string]any{
				"matchLabels": appSelector(opts),
			},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": appSelector(opts),
				},
				"spec": podSpec,
			},
			"volumeClaimTemplates": generateVolumeClaimTemplates(opts),
		}
		return baseManifest("apps/v1", "StatefulSet", opts, spec), kind
	default: // Deployment
		spec := map[string]any{
			"replicas": effectiveReplicas(opts),
			"selector": map[string]any{
				"matchLabels": appSelector(opts),
			},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": appSelector(opts),
				},
				"spec": podSpec,
			},
		}
		return baseManifest("apps/v1", "Deployment", opts, spec), "Deployment"
	}
}

func effectiveReplicas(opts spec.K8sGenInput) int {
	if opts.Deploy.Replica > 0 {
		return opts.Deploy.Replica
	}
	return 1
}

func appSelector(opts spec.K8sGenInput) map[string]string {
	return map[string]string{"app": opts.DeploymentName}
}

func baseManifest(apiVersion, kind string, opts spec.K8sGenInput, spec any) map[string]any {
	meta := map[string]any{
		"name": opts.DeploymentName,
	}
	if labels := mergedLabels(opts); len(labels) > 0 {
		meta["labels"] = labels
	}
	return map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   meta,
		"spec":       spec,
	}
}

func mergedLabels(opts spec.K8sGenInput) map[string]string {
	out := map[string]string{"app": opts.DeploymentName}
	maps.Copy(out, opts.Cluster.Defaults.Labels)
	return out
}

func deployNamespace(opts spec.K8sGenInput) string {
	if k8s := opts.Deploy.Kubernetes; k8s != nil && k8s.Namespace != "" {
		return k8s.Namespace
	}
	if opts.Cluster.DefaultNamespace != "" {
		return opts.Cluster.DefaultNamespace
	}
	return ""
}

// -----------------------------------------------------------------------------
// Pod spec.
// -----------------------------------------------------------------------------

func generatePodSpec(opts spec.K8sGenInput) map[string]any {
	container := map[string]any{
		"name":  opts.DeploymentName,
		"image": opts.ImageRef,
	}

	// Image pull policy from cluster profile.
	if opts.Cluster.ImageDefault.PullPolicy != "" {
		container["imagePullPolicy"] = opts.Cluster.ImageDefault.PullPolicy
	}

	// Container ports from capabilities.Ports
	if ports := generateContainerPorts(opts.Ports); len(ports) > 0 {
		container["ports"] = ports
	}

	// Env from deployment.Env
	if env := generateEnv(opts.Deploy.Env); len(env) > 0 {
		container["env"] = env
	}

	// Resources: requests from deployment.Resources, limits from Security
	resources := generateResources(opts.Deploy)
	if len(resources) > 0 {
		container["resources"] = resources
	}

	// Volume mounts from storage entries
	mounts := generateVolumeMounts(opts.Deploy)
	if len(mounts) > 0 {
		container["volumeMounts"] = mounts
	}

	// Probes (target-agnostic → K8s probe translation)
	if p := opts.Deploy.Probes; p != nil {
		if lp := checkToProbe(p.Liveness); lp != nil {
			container["livenessProbe"] = lp
		}
		if rp := checkToProbe(p.Readiness); rp != nil {
			container["readinessProbe"] = rp
		}
		if sp := checkToProbe(p.Startup); sp != nil {
			container["startupProbe"] = sp
		}
	}

	podSpec := map[string]any{
		"containers": []any{container},
	}

	// Volumes (non-StatefulSet mounts reference PVCs declared at pod level)
	if vols := generatePodVolumes(opts); len(vols) > 0 {
		podSpec["volumes"] = vols
	}

	// Image pull secrets
	if len(opts.Cluster.ImageDefault.PullSecrets) > 0 {
		refs := make([]map[string]any, 0, len(opts.Cluster.ImageDefault.PullSecrets))
		for _, s := range opts.Cluster.ImageDefault.PullSecrets {
			refs = append(refs, map[string]any{"name": s})
		}
		podSpec["imagePullSecrets"] = refs
	}

	// Pod-level security context from cluster profile admission policy.
	if sc := podSecurityContext(opts); len(sc) > 0 {
		podSpec["securityContext"] = sc
	}

	// Priority class / tolerations / node selector from cluster defaults.
	if pc := opts.Cluster.PodDefault.PriorityClass; pc != "" {
		podSpec["priorityClassName"] = pc
	}
	if tol := opts.Cluster.PodDefault.Tolerations; len(tol) > 0 {
		podSpec["tolerations"] = tol
	}
	if ns := opts.Cluster.PodDefault.NodeSelector; len(ns) > 0 {
		podSpec["nodeSelector"] = ns
	}

	// Restart policy (only meaningful on Pod/Job).
	kind := selectWorkloadKind(opts)
	if kind == "Pod" || kind == "Job" || kind == "CronJob" {
		rp := opts.Deploy.Restart
		if rp == "" {
			if kind == "Pod" {
				rp = "Always"
			} else {
				rp = "OnFailure"
			}
		}
		rpClean := strings.ReplaceAll(rp, "-", "")
		if rpClean != "" {
			rpClean = strings.ToUpper(rpClean[:1]) + rpClean[1:]
		}
		podSpec["restartPolicy"] = rpClean
	}

	return podSpec
}

func podSecurityContext(opts spec.K8sGenInput) map[string]any {
	out := map[string]any{}
	if opts.UID > 0 {
		out["runAsUser"] = opts.UID
	}
	if opts.GID > 0 {
		out["runAsGroup"] = opts.GID
		out["fsGroup"] = opts.GID
	}
	if opts.Cluster.AdmissionPolicy == "restricted" {
		out["runAsNonRoot"] = true
		out["seccompProfile"] = map[string]any{"type": "RuntimeDefault"}
	}
	return out
}

// -----------------------------------------------------------------------------
// Ports / Service.
// -----------------------------------------------------------------------------

func generateContainerPorts(ports []string) []map[string]any {
	var out []map[string]any
	for _, p := range ports {
		// ports entries look like "8080" or "8080/udp".
		port := p
		proto := "TCP"
		if idx := strings.IndexByte(p, '/'); idx > 0 {
			port = p[:idx]
			if strings.ToLower(p[idx+1:]) == "udp" {
				proto = "UDP"
			}
		}
		n, err := strconv.Atoi(port)
		if err != nil {
			continue
		}
		entry := map[string]any{
			"containerPort": n,
			"protocol":      proto,
		}
		out = append(out, entry)
	}
	return out
}

func generateService(opts spec.K8sGenInput, _ string) map[string]any {
	ports := generateContainerPorts(opts.Ports)
	if len(ports) == 0 {
		return nil
	}
	var servicePorts []map[string]any
	for _, p := range ports {
		sp := map[string]any{
			"port":       p["containerPort"],
			"targetPort": p["containerPort"],
			"protocol":   p["protocol"],
		}
		servicePorts = append(servicePorts, sp)
	}
	svcSpec := map[string]any{
		"selector": appSelector(opts),
		"ports":    servicePorts,
		"type":     "ClusterIP",
	}
	// For a StatefulSet the headless companion Service is emitted separately;
	// this is the regular ClusterIP one. (A follow-up could emit a second
	// headless Service named <name>-headless.)
	return baseManifest("v1", "Service", opts, svcSpec)
}

// -----------------------------------------------------------------------------
// Env.
// -----------------------------------------------------------------------------

func generateEnv(env map[string]string) []map[string]any {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{
			"name":  k,
			"value": env[k],
		})
	}
	return out
}

// -----------------------------------------------------------------------------
// Resources.
// -----------------------------------------------------------------------------

func generateResources(d spec.Deploy) map[string]any {
	out := map[string]any{}
	req := map[string]any{}
	if d.Resources != nil {
		if d.Resources.CPURequest != "" {
			req["cpu"] = d.Resources.CPURequest
		}
		if d.Resources.MemoryRequest != "" {
			req["memory"] = d.Resources.MemoryRequest
		}
	}
	limits := map[string]any{}
	if d.Security != nil {
		if d.Security.Cpus != "" {
			limits["cpu"] = d.Security.Cpus
		}
		if d.Security.MemoryMax != "" {
			limits["memory"] = d.Security.MemoryMax
		}
	}
	if len(req) > 0 {
		out["requests"] = req
	}
	if len(limits) > 0 {
		out["limits"] = limits
	}
	return out
}

// -----------------------------------------------------------------------------
// Storage / PVC / Volumes.
// -----------------------------------------------------------------------------

func storageClass(cluster spec.K8s, hint string) string {
	switch hint {
	case "fast":
		if cluster.Storage.ClassFast != "" {
			return cluster.Storage.ClassFast
		}
	case "cheap":
		if cluster.Storage.ClassCheap != "" {
			return cluster.Storage.ClassCheap
		}
	case "encrypted":
		if cluster.Storage.ClassEncrypted != "" {
			return cluster.Storage.ClassEncrypted
		}
	}
	return cluster.Storage.ClassDefault
}

func accessMode(cluster spec.K8s, access string) string {
	switch access {
	case "many-readers":
		return "ReadOnlyMany"
	case "many-writers":
		return "ReadWriteMany"
	case "single-writer":
		return "ReadWriteOnce"
	}
	if cluster.Storage.AccessModeDefault != "" {
		return cluster.Storage.AccessModeDefault
	}
	return "ReadWriteOnce"
}

func generatePVCs(opts spec.K8sGenInput) []map[string]any {
	out := make([]map[string]any, 0, len(opts.Deploy.Storage))
	for _, s := range opts.Deploy.Storage {
		spc := map[string]any{
			"accessModes": []string{accessMode(opts.Cluster, s.Access)},
		}
		size := s.Size
		if size == "" {
			size = "10Gi"
		}
		spc["resources"] = map[string]any{
			"requests": map[string]any{"storage": size},
		}
		if sc := storageClass(opts.Cluster, s.ClassHint); sc != "" {
			spc["storageClassName"] = sc
		}
		pvcOpts := opts
		pvcOpts.DeploymentName = opts.DeploymentName + "-" + s.Name
		pvc := baseManifest("v1", "PersistentVolumeClaim", pvcOpts, spc)
		out = append(out, pvc)
	}
	return out
}

func generateVolumeClaimTemplates(opts spec.K8sGenInput) []map[string]any {
	out := make([]map[string]any, 0, len(opts.Deploy.Storage))
	for _, s := range opts.Deploy.Storage {
		spc := map[string]any{
			"accessModes": []string{accessMode(opts.Cluster, s.Access)},
		}
		size := s.Size
		if size == "" {
			size = "10Gi"
		}
		spc["resources"] = map[string]any{
			"requests": map[string]any{"storage": size},
		}
		if sc := storageClass(opts.Cluster, s.ClassHint); sc != "" {
			spc["storageClassName"] = sc
		}
		out = append(out, map[string]any{
			"metadata": map[string]any{"name": s.Name},
			"spec":     spc,
		})
	}
	return out
}

func generateVolumeMounts(d spec.Deploy) []map[string]any {
	out := make([]map[string]any, 0, len(d.Storage))
	for _, s := range d.Storage {
		mount := s.Path
		if mount == "" {
			mount = "/var/lib/" + s.Name
		}
		out = append(out, map[string]any{
			"name":      s.Name,
			"mountPath": mount,
		})
	}
	return out
}

func generatePodVolumes(opts spec.K8sGenInput) []map[string]any {
	kind := selectWorkloadKind(opts)
	if kind == "StatefulSet" {
		// StatefulSet uses volumeClaimTemplates — no need for pod-level volumes.
		return nil
	}
	var out []map[string]any
	for _, s := range opts.Deploy.Storage {
		out = append(out, map[string]any{
			"name": s.Name,
			"persistentVolumeClaim": map[string]any{
				"claimName": opts.DeploymentName + "-" + s.Name,
			},
		})
	}
	return out
}

// -----------------------------------------------------------------------------
// Ingress.
// -----------------------------------------------------------------------------

func generateIngress(opts spec.K8sGenInput) map[string]any {
	expose := opts.Deploy.Expose
	if expose == nil || expose.Host == "" {
		return nil
	}
	if !opts.Cluster.Ingress.Enabled {
		return nil // cluster profile opts out of ingress generation
	}

	pathType := opts.Cluster.Ingress.PathTypeDefault
	if pathType == "" {
		pathType = "Prefix"
	}
	path := expose.Path
	if path == "" {
		path = "/"
	}
	portValue := resolveIngressPort(opts, expose.Port)

	rule := map[string]any{
		"host": expose.Host,
		"http": map[string]any{
			"paths": []any{
				map[string]any{
					"path":     path,
					"pathType": pathType,
					"backend": map[string]any{
						"service": map[string]any{
							"name": opts.DeploymentName,
							"port": map[string]any{"number": portValue},
						},
					},
				},
			},
		},
	}

	spc := map[string]any{
		"rules": []any{rule},
	}
	if opts.Cluster.Ingress.Class != "" {
		spc["ingressClassName"] = opts.Cluster.Ingress.Class
	}
	if expose.TLS {
		spc["tls"] = []any{
			map[string]any{
				"hosts":      []string{expose.Host},
				"secretName": opts.DeploymentName + "-tls",
			},
		}
	}

	meta := map[string]any{
		"name": opts.DeploymentName,
	}
	if labels := mergedLabels(opts); len(labels) > 0 {
		meta["labels"] = labels
	}
	if expose.TLS && opts.Cluster.Ingress.CertIssuer != "" {
		meta["annotations"] = map[string]string{
			"cert-manager.io/cluster-issuer": opts.Cluster.Ingress.CertIssuer,
		}
	}
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   meta,
		"spec":       spc,
	}
}

func resolveIngressPort(opts spec.K8sGenInput, portNameOrNumber string) int {
	// If the deployment names a port by number, parse it.
	if n, err := strconv.Atoi(portNameOrNumber); err == nil && n > 0 {
		return n
	}
	// Fall back to the first capability port.
	for _, p := range opts.Ports {
		raw := p
		if idx := strings.IndexByte(p, '/'); idx > 0 {
			raw = p[:idx]
		}
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// -----------------------------------------------------------------------------
// Check → K8s Probe translation.
// -----------------------------------------------------------------------------

// checkToProbe turns a generic declarative Check into a K8s probe spec.
// Translates the four most common check types into Kubernetes-native
// probe shapes:
//
//   - http: <url>          → httpGet { path, port [+ host] }
//   - addr: <host:port>    → tcpSocket { port [+ host] }
//   - file: <path>         → exec { command: ["test", "-e", path] }
//   - command: <cmd>       → exec { command: ["sh", "-c", cmd] }
//
// Returns nil for unsupported / unset checks so the manifest emitter
// can omit the probe entirely (kubectl tolerates a missing probe; an
// empty map would be rendered as `livenessProbe: {}` and rejected by
// the apiserver's schema).
func checkToProbe(c *spec.Op) map[string]any {
	if c == nil {
		return nil
	}
	// http / addr are observe-only verbs extracted to builtin plugin units, so their
	// discriminator now rides plugin_input (op.HTTP / op.Addr left #Op). Read it via
	// pluginInputStr so a migrated `plugin: http` / `plugin: addr` check step still
	// generates the httpGet / tcpSocket probe (else the probe would silently vanish).
	httpURL := c.PluginInputStr("http")
	addrTarget := c.PluginInputStr("addr")
	// `file` is a plugin verb now — its path rides plugin_input (the file-exclusive
	// discriminator left #Op into #FileInput).
	fileTarget := c.PluginInputStr("file")
	// `command` is a plugin verb now — its body rides plugin_input (with a literal
	// Op.Command fallback for a directly-constructed probe Op).
	cmdStr := c.Command
	if cmdStr == "" {
		cmdStr = c.PluginInputStr("command")
	}
	switch {
	case httpURL != "":
		probe := map[string]any{}
		// HTTP url shape: scheme://host:port/path. We extract path+port
		// best-effort; on parse failure we still emit httpGet with the
		// raw string under "path" so the manifest is recognisable.
		path, port, host := parseHTTPForProbe(httpURL)
		probe["path"] = path
		probe["port"] = port
		if host != "" {
			probe["host"] = host
		}
		return map[string]any{"httpGet": probe}
	case addrTarget != "":
		host, port := parseAddrForProbe(addrTarget)
		probe := map[string]any{"port": port}
		if host != "" {
			probe["host"] = host
		}
		return map[string]any{"tcpSocket": probe}
	case fileTarget != "":
		// Probe semantics: file exists. K8s exec probes succeed iff
		// exit 0; `test -e <path>` matches that contract.
		return map[string]any{"exec": map[string]any{"command": []string{"test", "-e", fileTarget}}}
	case cmdStr != "":
		// `sh -c <cmd>` because the command often carries pipelines or
		// shell-substitutions that bare exec wouldn't handle.
		return map[string]any{"exec": map[string]any{"command": []string{"sh", "-c", cmdStr}}}
	}
	return nil
}

// parseHTTPForProbe extracts (path, port, host) from a check's HTTP URL.
// Defaults: path "/", port 80 for http / 443 for https, host empty (k8s
// uses the pod IP). Best-effort — on parse failure returns the URL as
// path with port 80 so the user sees something traceable.
func parseHTTPForProbe(url string) (path string, port int, host string) {
	path, port = "/", 80
	rest := url
	if after, ok := strings.CutPrefix(rest, "https://"); ok {
		rest = after
		port = 443
	} else if after, ok := strings.CutPrefix(rest, "http://"); ok {
		rest = after
	}
	// rest = host[:port][/path]
	pathIdx := strings.Index(rest, "/")
	hostPort := rest
	if pathIdx >= 0 {
		hostPort = rest[:pathIdx]
		path = rest[pathIdx:]
	}
	if colonIdx := strings.LastIndex(hostPort, ":"); colonIdx >= 0 {
		host = hostPort[:colonIdx]
		if p, err := strconv.Atoi(hostPort[colonIdx+1:]); err == nil {
			port = p
		}
	} else {
		host = hostPort
	}
	// k8s probes use the pod IP by default — leave host empty when the
	// caller used "localhost" or "127.0.0.1" since those mean "the pod"
	// in container-probe semantics.
	if host == "localhost" || host == "127.0.0.1" {
		host = ""
	}
	return path, port, host
}

// parseAddrForProbe splits "host:port" → (host, port). Host may be
// empty when the check used a bare port (interpreted as pod-local).
func parseAddrForProbe(addr string) (host string, port int) {
	if colonIdx := strings.LastIndex(addr, ":"); colonIdx >= 0 {
		host = addr[:colonIdx]
		if p, err := strconv.Atoi(addr[colonIdx+1:]); err == nil {
			port = p
		}
	} else if p, err := strconv.Atoi(addr); err == nil {
		port = p
	}
	if host == "localhost" || host == "127.0.0.1" {
		host = ""
	}
	return host, port
}
