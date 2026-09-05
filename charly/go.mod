module github.com/opencharly/charly/charly

go 1.26.4

require (
	cuelang.org/go v0.16.1
	github.com/alecthomas/kong v1.15.0
	github.com/hashicorp/go-plugin v1.8.0
	github.com/opencharly/plugin-addr/candy/plugin-addr v0.2026237.1413
	github.com/opencharly/plugin-agent-pi/candy/plugin-agent-pi v0.2026237.1414
	github.com/opencharly/plugin-agent/candy/plugin-agent v0.2026237.1413
	github.com/opencharly/plugin-agentteams/candy/plugin-agentteams v0.2026237.1414
	github.com/opencharly/plugin-alias/candy/plugin-alias v0.2026237.1414
	github.com/opencharly/plugin-authoring/candy/plugin-authoring v0.2026237.1414
	github.com/opencharly/plugin-box/candy/plugin-box v0.2026243.1718
	github.com/opencharly/plugin-build/candy/plugin-build v0.2026243.1627
	github.com/opencharly/plugin-builder/candy/plugin-builder v0.2026237.1415
	github.com/opencharly/plugin-candy-kind/candy/plugin-candy-kind v0.2026237.1416
	github.com/opencharly/plugin-candy/candy/plugin-candy v0.2026237.1416
	github.com/opencharly/plugin-check/candy/plugin-check v0.2026244.1307
	github.com/opencharly/plugin-clean/candy/plugin-clean v0.2026237.1417
	github.com/opencharly/plugin-cmd/candy/plugin-cmd v0.2026237.1417
	github.com/opencharly/plugin-command/candy/plugin-command v0.2026244.617
	github.com/opencharly/plugin-distro/candy/plugin-distro v0.2026242.1131
	github.com/opencharly/plugin-dns/candy/plugin-dns v0.2026237.1418
	github.com/opencharly/plugin-doctor/candy/plugin-doctor v0.2026237.1419
	github.com/opencharly/plugin-dsh/candy/plugin-dsh v0.2026237.1419
	github.com/opencharly/plugin-egress/candy/plugin-egress v0.2026237.1419
	github.com/opencharly/plugin-enc/candy/plugin-enc v0.2026237.1419
	github.com/opencharly/plugin-example-bootstrap/candy/plugin-example-bootstrap v0.2026237.1419
	github.com/opencharly/plugin-example-command/candy/plugin-example-command v0.2026237.1420
	github.com/opencharly/plugin-example-external/candy/plugin-example-external v0.2026237.1420
	github.com/opencharly/plugin-example/candy/plugin-example v0.2026237.1419
	github.com/opencharly/plugin-examplerunverb/candy/plugin-examplerunverb v0.2026237.1421
	github.com/opencharly/plugin-feature/candy/plugin-feature v0.2026237.1422
	github.com/opencharly/plugin-file/candy/plugin-file v0.2026242.2145
	github.com/opencharly/plugin-fleet/candy/plugin-fleet v0.2026241.1037
	github.com/opencharly/plugin-gpu/candy/plugin-gpu v0.2026237.1422
	github.com/opencharly/plugin-group/candy/plugin-group v0.2026237.1423
	github.com/opencharly/plugin-harness-kind/candy/plugin-harness-kind v0.2026237.1423
	github.com/opencharly/plugin-http/candy/plugin-http v0.2026237.1423
	github.com/opencharly/plugin-init/candy/plugin-init v0.2026240.1727
	github.com/opencharly/plugin-installstep/candy/plugin-installstep v0.2026237.1424
	github.com/opencharly/plugin-interface/candy/plugin-interface v0.2026237.1424
	github.com/opencharly/plugin-k8sgen/candy/plugin-k8sgen v0.2026237.1424
	github.com/opencharly/plugin-kernel-param/candy/plugin-kernel-param v0.2026237.1424
	github.com/opencharly/plugin-loader/candy/plugin-loader v0.2026243.719
	github.com/opencharly/plugin-matching/candy/plugin-matching v0.2026237.1425
	github.com/opencharly/plugin-migrate/candy/plugin-migrate v0.2026237.1425
	github.com/opencharly/plugin-mount/candy/plugin-mount v0.2026237.1426
	github.com/opencharly/plugin-oci/candy/plugin-oci v0.2026237.1426
	github.com/opencharly/plugin-ollama/candy/plugin-ollama v0.2026237.1426
	github.com/opencharly/plugin-package/candy/plugin-package v0.2026237.1426
	github.com/opencharly/plugin-pod/candy/plugin-pod v0.2026237.1426
	github.com/opencharly/plugin-port/candy/plugin-port v0.2026237.1411
	github.com/opencharly/plugin-preempt/candy/plugin-preempt v0.2026237.1426
	github.com/opencharly/plugin-process/candy/plugin-process v0.2026237.1427
	github.com/opencharly/plugin-refs/candy/plugin-refs v0.2026237.1427
	github.com/opencharly/plugin-resource/candy/plugin-resource v0.2026237.1427
	github.com/opencharly/plugin-service/candy/plugin-service v0.2026242.2146
	github.com/opencharly/plugin-settings/candy/plugin-settings v0.2026237.1428
	github.com/opencharly/plugin-sidecar/candy/plugin-sidecar v0.2026237.1428
	github.com/opencharly/plugin-ssh/candy/plugin-ssh v0.2026237.1428
	github.com/opencharly/plugin-status/candy/plugin-status v0.2026237.1429
	github.com/opencharly/plugin-substrate/candy/plugin-substrate v0.2026241.1038
	github.com/opencharly/plugin-tmux/candy/plugin-tmux v0.2026237.1429
	github.com/opencharly/plugin-tunnel/candy/plugin-tunnel v0.2026237.1429
	github.com/opencharly/plugin-unix-group/candy/plugin-unix-group v0.2026237.1430
	github.com/opencharly/plugin-user/candy/plugin-user v0.2026242.2148
	github.com/opencharly/plugin-vm/candy/plugin-vm v0.2026246.545
	golang.org/x/term v0.43.0
	google.golang.org/grpc v1.61.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
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
	github.com/digitalocean/go-libvirt v0.0.0-20260217163227-273eaa321819 // indirect
	github.com/docker/cli v29.0.3+incompatible // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.3 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/google/go-containerregistry v0.20.7 // indirect
	github.com/google/renameio/v2 v2.0.2 // indirect
	github.com/kata-containers/govmm v0.0.0-20220119175834-88960a15dacd // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/ollama/ollama v0.32.14 // indirect
	github.com/opencharly/sdk v0.2026243.714 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/vbatts/tar-split v0.12.2 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	libvirt.org/go/libvirtxml v1.12005.0 // indirect
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
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/opencharly/spec v0.2026247.2350
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/protocolbuffers/txtpbfmt v0.0.0-20260217160748-a481f6a22f94 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
)

