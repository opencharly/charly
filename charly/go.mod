module github.com/opencharly/charly/charly

go 1.26.0

require (
	cuelang.org/go v0.16.1
	github.com/alecthomas/kong v1.15.0
	github.com/hashicorp/go-plugin v1.8.0
	github.com/opencharly/charly/candy/plugin-addr v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-agent v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-agent-pi v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-alias v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-authoring v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-box v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-build v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-builder v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-candy v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-candy-kind v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-check v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-clean v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-cmd v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-command v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-distro v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-dns v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-doctor v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-egress v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-enc v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-example v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-example-bootstrap v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-example-command v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-example-external v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-examplerunverb v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-feature v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-file v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-gpu v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-group v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-http v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-init v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-installstep v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-interface v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-k8sgen v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-kernel-param v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-loader v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-matching v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-migrate v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-mount v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-oci v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-package v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-pod v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-port v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-preempt v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-process v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-refs v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-resource v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-service v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-settings v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-sidecar v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-ssh v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-status v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-substrate v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-tmux v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-tunnel v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-unix-group v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-user v0.0.0-20260729064511-fdae3cc3d4dd
	github.com/opencharly/charly/candy/plugin-vm v0.0.0-20260729064511-fdae3cc3d4dd
	golang.org/x/term v0.41.0
	google.golang.org/grpc v1.61.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/charmbracelet/colorprofile v0.4.2 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260303162955-0b88c25f3fff // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/vt v0.0.0-20260615092313-b57e5e6d29bb // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/containerd/stargz-snapshotter/estargz v0.18.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/digitalocean/go-libvirt v0.0.0-20260217163227-273eaa321819 // indirect
	github.com/docker/cli v29.0.3+incompatible // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.3 // indirect
	github.com/google/go-containerregistry v0.20.7 // indirect
	github.com/kata-containers/govmm v0.0.0-20220119175834-88960a15dacd // indirect
	github.com/klauspost/compress v1.18.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/vbatts/tar-split v0.12.2 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	libvirt.org/go/libvirtxml v1.12002.0 // indirect
)

require (
	github.com/cockroachdb/apd/v3 v3.2.1 // indirect
	github.com/emicklei/proto v1.14.3 // indirect
	github.com/fatih/color v1.15.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-hclog v1.6.3
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/opencharly/spec v0.2026224.1942
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/protocolbuffers/txtpbfmt v0.0.0-20260217160748-a481f6a22f94 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
)

replace github.com/opencharly/spec => ../spec
