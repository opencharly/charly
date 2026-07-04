package main

import "testing"

// testPubKey is the SSH test pubkey for the cloud-init egress render test
// (TestRenderCloudInit_OutputValidatesAgainstSchema). Formerly shared from
// cloud_init_render_test.go, which relocated to sdk/vmshared/.
const testPubKey = "ssh-ed25519 AAAATESTKEY user@host"

// Egress-validation coverage. The teeth tests (the *BadFails cases) are the ones
// that would PASS — wrongly — if the egress gate did not exist: they assert that
// a malformed artifact is REJECTED before it could ever be written.

func TestValidateEgress_CloudConfigGoodPasses(t *testing.T) {
	good := []byte(`hostname: vm1
users:
  - default
  - name: charly
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    groups: [wheel]
    shell: /bin/bash
    ssh_authorized_keys:
      - "ssh-ed25519 AAAAtest charly@host"
packages:
  - openssh
  - curl
runcmd:
  - [systemctl, enable, --now, sshd]
`)
	if err := ValidateEgress("cloud_config", "good user-data", good); err != nil {
		t.Fatalf("good cloud-config should validate, got: %v", err)
	}
}

func TestValidateEgress_CloudConfigBadFails(t *testing.T) {
	// package_update must be a bool; packages must be a list — both type-violated.
	bad := []byte("package_update: \"yes\"\npackages: 12345\n")
	if err := ValidateEgress("cloud_config", "bad user-data", bad); err == nil {
		t.Fatal("malformed cloud-config (string package_update, int packages) must be REJECTED, got nil")
	}
}

func TestValidateEgress_CloudInitMeta(t *testing.T) {
	if err := ValidateEgress("cloud_init_meta", "good meta", []byte("instance-id: iid-1\nlocal-hostname: vm1\n")); err != nil {
		t.Fatalf("good meta-data should validate, got: %v", err)
	}
	// #CloudInitMeta is closed → an unknown key is rejected.
	if err := ValidateEgress("cloud_init_meta", "bad meta", []byte("bogus-key: x\n")); err == nil {
		t.Fatal("meta-data with an unknown key must be REJECTED (closed schema), got nil")
	}
}

func TestValidateEgress_NetworkConfig(t *testing.T) {
	if err := ValidateEgress("cloud_init_net", "good net", []byte("version: 2\nethernets:\n  eth0: {dhcp4: true}\n")); err != nil {
		t.Fatalf("good network-config should validate, got: %v", err)
	}
	if err := ValidateEgress("cloud_init_net", "bad net", []byte("version: 2\nbogus: 1\n")); err == nil {
		t.Fatal("network-config with an unknown top-level key must be REJECTED (closed schema), got nil")
	}
}

func TestValidateEgress_UnknownKind(t *testing.T) {
	if err := ValidateEgress("no-such-kind", "x", []byte("a: 1\n")); err == nil {
		t.Fatal("unknown egress kind must error, got nil")
	}
}

func TestValidateEgressValue_K8sObject(t *testing.T) {
	good := map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec":     map[string]any{"replicas": 2},
	}
	if err := ValidateEgressValue("k8s_object", "good deployment", good); err != nil {
		t.Fatalf("valid k8s object should pass, got: %v", err)
	}
	// teeth: empty kind, and missing metadata.name, must be rejected.
	for name, bad := range map[string]map[string]any{
		"empty-kind": {"apiVersion": "v1", "kind": "", "metadata": map[string]any{"name": "x"}},
		"no-name":    {"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{}},
		"no-apiVer":  {"kind": "Service", "metadata": map[string]any{"name": "x"}},
	} {
		if err := ValidateEgressValue("k8s_object", name, bad); err == nil {
			t.Fatalf("malformed k8s object %q must be REJECTED, got nil", name)
		}
	}
}

func TestValidateEgressValue_Kustomization(t *testing.T) {
	good := map[string]any{
		"apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "Kustomization",
		"resources": []string{"deployment.yaml", "service.yaml"},
	}
	if err := ValidateEgressValue("kustomization", "good kustomization", good); err != nil {
		t.Fatalf("valid kustomization should pass, got: %v", err)
	}
	bad := map[string]any{"apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "NotKustomization", "resources": []string{}}
	if err := ValidateEgressValue("kustomization", "bad kustomization", bad); err == nil {
		t.Fatal("kustomization with wrong kind must be REJECTED, got nil")
	}
}

func TestValidateEgressValue_DeployRecord(t *testing.T) {
	good := &DeployRecord{
		SchemaVersion: ledgerSchemaVersion, DeployID: "abc123", Image: "ghcr.io/x/y:tag",
		Target: "host", Candy: []string{"ripgrep"}, DeployedAt: "2026-06-15T00:00:00Z",
	}
	if err := ValidateEgressValue("deploy_record", "good deploy rec", good); err != nil {
		t.Fatalf("valid deploy record should pass, got: %v", err)
	}
	bad := &DeployRecord{Image: "x", Target: "host", DeployedAt: "t"} // empty DeployID
	if err := ValidateEgressValue("deploy_record", "bad deploy rec", bad); err == nil {
		t.Fatal("deploy record with empty deploy_id must be REJECTED, got nil")
	}
}

func TestValidateEgressValue_CandyRecord(t *testing.T) {
	good := &CandyRecord{
		SchemaVersion: ledgerSchemaVersion, Candy: "ripgrep",
		DeployedBy: []string{"abc123"}, DeployedAt: "2026-06-15T00:00:00Z",
	}
	if err := ValidateEgressValue("candy_record", "good candy rec", good); err != nil {
		t.Fatalf("valid candy record should pass, got: %v", err)
	}
	bad := &CandyRecord{DeployedAt: "t"} // empty Candy
	if err := ValidateEgressValue("candy_record", "bad candy rec", bad); err == nil {
		t.Fatal("candy record with empty candy must be REJECTED, got nil")
	}
}

func TestValidateEgress_TraefikRoutes(t *testing.T) {
	good := []byte("http:\n  routers:\n    app:\n      rule: \"Host(`app.example.com`)\"\n      service: app\n  services:\n    app:\n      loadBalancer:\n        servers:\n          - url: \"http://127.0.0.1:8080\"\n")
	if err := ValidateEgress("traefik_routes", "good routes", good); err != nil {
		t.Fatalf("valid traefik routes should pass, got: %v", err)
	}
	// empty routers/services (traefik composed but no route candies) must pass.
	empty := []byte("http:\n  routers:\n  services:\n")
	if err := ValidateEgress("traefik_routes", "empty routes", empty); err != nil {
		t.Fatalf("empty traefik routes (null routers/services) should pass, got: %v", err)
	}
	// teeth: an empty service backend url must be rejected.
	bad := []byte("http:\n  routers:\n    app:\n      rule: \"Host(`x`)\"\n      service: app\n  services:\n    app:\n      loadBalancer:\n        servers:\n          - url: \"\"\n")
	if err := ValidateEgress("traefik_routes", "bad routes", bad); err == nil {
		t.Fatal("traefik routes with an empty backend url must be REJECTED, got nil")
	}
}

func TestValidateTextEgress_RenderedText(t *testing.T) {
	good := "FROM fedora:43\nRUN dnf install -y git\nUSER 1000\n"
	if err := validateTextEgress("good containerfile", good); err != nil {
		t.Fatalf("clean rendered text should pass, got: %v", err)
	}
	// teeth: a Go text/template nil-field marker means a render failure.
	bad := "[Service]\nExecStart=<no value>\nRestart=always\n"
	if err := validateTextEgress("broken unit", bad); err == nil {
		t.Fatal("rendered text containing the template-failure marker <no value> must be REJECTED, got nil")
	}
}

func TestValidateXMLEgress_LibvirtDomain(t *testing.T) {
	good := "<domain type='kvm'>\n  <name>vm1</name>\n  <memory unit='KiB'>8388608</memory>\n  <os><type>hvm</type></os>\n</domain>\n"
	if err := ValidateXMLEgress("libvirt_domain_xml", "good domain", good); err != nil {
		t.Fatalf("valid libvirt domain XML should pass, got: %v", err)
	}
	// teeth: an empty <name> is a malformed domain — koala decodes it, schema rejects.
	bad := "<domain type='kvm'>\n  <name></name>\n  <memory unit='KiB'>8388608</memory>\n</domain>\n"
	if err := ValidateXMLEgress("libvirt_domain_xml", "bad domain", bad); err == nil {
		t.Fatal("libvirt domain XML with an empty <name> must be REJECTED, got nil")
	}
	// best-effort: junk that koala cannot decode must NOT hard-fail (defer to libvirt).
	if err := ValidateXMLEgress("libvirt_domain_xml", "undecodable", "not xml at all <<<"); err != nil {
		t.Fatalf("undecodable input should be best-effort skipped (nil), got: %v", err)
	}
}

// TestRenderCloudInit_OutputValidatesAgainstSchema proves the renderer's real
// output satisfies the egress gate end to end (RenderCloudInit returns the gate's
// error directly, so a non-nil err here would mean charly emits cloud-init that
// its own vendored schema rejects).
func TestRenderCloudInit_OutputValidatesAgainstSchema(t *testing.T) {
	spec := &VmSpec{Source: VmSource{Kind: "cloud_image", Distro: "arch", BaseUser: "arch"}}
	rt := CloudInitRuntimeParams{SSHPublicKey: testPubKey, InjectKeyViaCloudInit: true, InstanceID: "iid-xyz", Hostname: "egress-vm"}
	if _, _, _, err := RenderCloudInit(spec, rt); err != nil {
		t.Fatalf("rendered cloud-init must pass its own egress gate, got: %v", err)
	}
}
