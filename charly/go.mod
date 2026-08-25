module github.com/opencharly/charly/charly

go 1.26.4

require (
	cuelang.org/go v0.16.1
	github.com/alecthomas/kong v1.15.0
	github.com/hashicorp/go-plugin v1.8.0
	github.com/opencharly/plugin-addr/candy/plugin-addr v0.0.0-20260825141335-7b28a3e0ff5b
	github.com/opencharly/plugin-agent-pi/candy/plugin-agent-pi v0.0.0-20260825141400-ae910517c678
	github.com/opencharly/plugin-agent/candy/plugin-agent v0.0.0-20260825141346-47b462850b19
	github.com/opencharly/plugin-agentteams/candy/plugin-agentteams v0.0.0-20260825141411-08bdbc6baecd
	github.com/opencharly/plugin-alias/candy/plugin-alias v0.0.0-20260825141422-9ec14336552d
	github.com/opencharly/plugin-authoring/candy/plugin-authoring v0.0.0-20260825141446-4f3c28ff9b28
	github.com/opencharly/plugin-box/candy/plugin-box v0.0.0-20260825141457-424588654dcd
	github.com/opencharly/plugin-build/candy/plugin-build v0.0.0-20260825141509-5036fb8c5c04
	github.com/opencharly/plugin-builder/candy/plugin-builder v0.0.0-20260825141520-76e6f092c296
	github.com/opencharly/plugin-candy-kind/candy/plugin-candy-kind v0.0.0-20260825141630-ca7d52d6b1b4
	github.com/opencharly/plugin-candy/candy/plugin-candy v0.0.0-20260825141618-271a31a9f5df
	github.com/opencharly/plugin-check/candy/plugin-check v0.0.0-20260825141653-0ab259ef8ce9
	github.com/opencharly/plugin-clean/candy/plugin-clean v0.0.0-20260825141705-a6b81c50da65
	github.com/opencharly/plugin-cmd/candy/plugin-cmd v0.0.0-20260825141716-ff8339d00728
	github.com/opencharly/plugin-command/candy/plugin-command v0.0.0-20260825141727-b7525b2a2903
	github.com/opencharly/plugin-distro/candy/plugin-distro v0.0.0-20260825141827-7e9d12855df2
	github.com/opencharly/plugin-dns/candy/plugin-dns v0.0.0-20260825141838-58409f8a2cbe
	github.com/opencharly/plugin-doctor/candy/plugin-doctor v0.0.0-20260825141900-ef8da58f19c0
	github.com/opencharly/plugin-dsh/candy/plugin-dsh v0.0.0-20260825141912-22e04f23e7c4
	github.com/opencharly/plugin-egress/candy/plugin-egress v0.0.0-20260825141923-6b0f10a2078d
	github.com/opencharly/plugin-enc/candy/plugin-enc v0.0.0-20260825141935-7f9158a8659b
	github.com/opencharly/plugin-example-bootstrap/candy/plugin-example-bootstrap v0.0.0-20260825141958-d3c29ebe6547
	github.com/opencharly/plugin-example-command/candy/plugin-example-command v0.0.0-20260825142021-e6e5fd87f97a
	github.com/opencharly/plugin-example-external/candy/plugin-example-external v0.0.0-20260825142055-132de8968d44
	github.com/opencharly/plugin-example/candy/plugin-example v0.0.0-20260825141947-7bd823c3cb97
	github.com/opencharly/plugin-examplerunverb/candy/plugin-examplerunverb v0.0.0-20260825142130-4ac4d38a258d
	github.com/opencharly/plugin-feature/candy/plugin-feature v0.0.0-20260825142217-86768d58d581
	github.com/opencharly/plugin-file/candy/plugin-file v0.0.0-20260825142228-d9e97deec4ae
	github.com/opencharly/plugin-fleet/candy/plugin-fleet v0.0.0-20260825142240-acd9c5363618
	github.com/opencharly/plugin-gpu/candy/plugin-gpu v0.0.0-20260825142253-a7d11feeabd4
	github.com/opencharly/plugin-group/candy/plugin-group v0.0.0-20260825142304-f5fa9c498dd7
	github.com/opencharly/plugin-harness-kind/candy/plugin-harness-kind v0.0.0-20260825142316-821568370819
	github.com/opencharly/plugin-http/candy/plugin-http v0.0.0-20260825142341-5bb42ef50aa2
	github.com/opencharly/plugin-init/candy/plugin-init v0.0.0-20260825142400-697bedac36f4
	github.com/opencharly/plugin-installstep/candy/plugin-installstep v0.0.0-20260825142411-74d8bf315e7e
	github.com/opencharly/plugin-interface/candy/plugin-interface v0.0.0-20260825142424-27d2322e2fa7
	github.com/opencharly/plugin-k8sgen/candy/plugin-k8sgen v0.0.0-20260825142435-dc8162cde1ab
	github.com/opencharly/plugin-kernel-param/candy/plugin-kernel-param v0.0.0-20260825142446-cda91e852733
	github.com/opencharly/plugin-loader/candy/plugin-loader v0.0.0-20260825142732-5efdba2eb3d9
	github.com/opencharly/plugin-matching/candy/plugin-matching v0.0.0-20260825142525-daee7ce0641f
	github.com/opencharly/plugin-migrate/candy/plugin-migrate v0.0.0-20260825142550-030486dafdfa
	github.com/opencharly/plugin-mount/candy/plugin-mount v0.0.0-20260825142602-1faabd1c2157
	github.com/opencharly/plugin-oci/candy/plugin-oci v0.0.0-20260825142614-7b9e07f85260
	github.com/opencharly/plugin-ollama/candy/plugin-ollama v0.0.0-20260825142625-f28af040d9d5
	github.com/opencharly/plugin-package/candy/plugin-package v0.0.0-20260825142636-6f6d59a781c0
	github.com/opencharly/plugin-pod/candy/plugin-pod v0.0.0-20260825142647-e2df6aba60f6
	github.com/opencharly/plugin-port/candy/plugin-port v0.0.0-20260825141139-87740ec75774
	github.com/opencharly/plugin-preempt/candy/plugin-preempt v0.0.0-20260825142659-0a409aa19bcd
	github.com/opencharly/plugin-process/candy/plugin-process v0.0.0-20260825142711-242979d696d6
	github.com/opencharly/plugin-refs/candy/plugin-refs v0.0.0-20260825142734-e34fc5dca682
	github.com/opencharly/plugin-resource/candy/plugin-resource v0.0.0-20260825142745-57dbc431fe72
	github.com/opencharly/plugin-service/candy/plugin-service v0.0.0-20260825142807-7cb6826ca84d
	github.com/opencharly/plugin-settings/candy/plugin-settings v0.0.0-20260825142819-f1b0034ea565
	github.com/opencharly/plugin-sidecar/candy/plugin-sidecar v0.0.0-20260825142830-9533a4105711
	github.com/opencharly/plugin-ssh/candy/plugin-ssh v0.0.0-20260825142853-ce8100c08ae9
	github.com/opencharly/plugin-status/candy/plugin-status v0.0.0-20260825142904-cc33b0dd5df7
	github.com/opencharly/plugin-substrate/candy/plugin-substrate v0.0.0-20260825142916-3846dacbd27a
	github.com/opencharly/plugin-tmux/candy/plugin-tmux v0.0.0-20260825142928-eab96ef226ed
	github.com/opencharly/plugin-tunnel/candy/plugin-tunnel v0.0.0-20260825142938-189793e8c482
	github.com/opencharly/plugin-unix-group/candy/plugin-unix-group v0.0.0-20260825143001-00c8f25c41e5
	github.com/opencharly/plugin-user/candy/plugin-user v0.0.0-20260825143013-cadd2545e04a
	github.com/opencharly/plugin-vm/candy/plugin-vm v0.0.0-20260825143024-f8c2cdc8b90a
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
	github.com/opencharly/sdk v0.2026236.1958 // indirect
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
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/opencharly/spec v0.2026232.521-0.20260824192047-0c29ab15816d
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/protocolbuffers/txtpbfmt v0.0.0-20260217160748-a481f6a22f94 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
)
