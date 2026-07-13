package main

// P8: charly *Candy accessors that satisfy the deploykit.CandyModel render-delta
// methods (the build-mode graph/render surface the relocated render engine reads
// through the interface). The three method-backed members (HasContent,
// PixiManifest, HasFormatPackages) already satisfy CandyModel directly; the
// field-backed members below use Get* to avoid a field/method name collision on
// *Candy. CandyRef is aliased to deploykit.CandyRef, so these signatures match.

func (l *Candy) GetIncludedCandy() []CandyRef { return l.IncludedCandy }
func (l *Candy) GetRequire() []CandyRef       { return l.Require }
func (l *Candy) GetHasPackageJson() bool      { return l.HasPackageJson }
func (l *Candy) GetHasCargoToml() bool        { return l.HasCargoToml }
func (l *Candy) GetExternalBuilder() string   { return l.ExternalBuilder }
func (l *Candy) GetRemote() bool              { return l.Remote }
func (l *Candy) GetHasPixiLock() bool         { return l.HasPixiLock }
func (l *Candy) GetRepoPath() string          { return l.RepoPath }
func (l *Candy) GetSubPathPrefix() string     { return l.SubPathPrefix }

// RelayPorts wraps the field-backed PortRelayPorts (field/method collision).
func (l *Candy) RelayPorts() []int { return l.PortRelayPorts }

// RunOps exports the plan→build-op lowering for the deploykit render (CandyModel).
func (l *Candy) RunOps() []Op { return l.runOps() }
