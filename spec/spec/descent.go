package spec

// descent.go — the venue-hop descent-descriptor stamper (the descent de-type,
// Cutover H; the trait SOURCE de-branched in P9). This is the SINGLE mechanism that
// projects a substrate's DECLARED #DeployTraits onto every node's #DescentDescriptor
// (R3): a substrate plugin (candy/plugin-substrate) advertises its per-word traits over
// Describe (ProvidedCapability.deploy_traits), the HOST resolves them by word through a
// registry-backed `traitsFor` callback (deployTraitsFor), and StampDescent stamps them
// here. Every consult site then reads the substrate behaviour off node.Descent BY TRAIT —
// never by switching on the substrate kind word. The transport SET is the kernel's closed
// nesting-boundary vocabulary (JumpKind); it is DERIVED from the declared traits by
// DescentFromTraits, so no word→transport mapping lives in the kernel or kit.
//
// Homed in spec (not sdk/kit) as a pure #Deploy-tree value transform over the E-envelope:
// it reads/writes only spec value types (Deploy/DeployTraits/DescentDescriptor), carries no
// host coupling, and is consumed by both charly core and deploy plugins — sdk/kit re-exports
// it (kit.StampDescent/DescentFromTraits) so existing plugin call sites are untouched.

// StampDescent stamps node.Descent from the substrate's DECLARED traits (resolved by
// `traitsFor(node.Target)`) and recurses into the whole nested (Children) + peer (Members)
// subtree, so every node the deploy chain can descend into carries a descriptor. Idempotent —
// re-stamping with the same traitsFor writes the same value. traitsFor must never be nil; it
// returns nil for a word with no declared substrate traits (a targetless group / empty target),
// which DescentFromTraits maps to the external-in-place default.
func StampDescent(node *Deploy, traitsFor func(word string) *DeployTraits) {
	if node == nil {
		return
	}
	node.Descent = DescentFromTraits(traitsFor(node.Target))
	for _, child := range node.Children {
		StampDescent(child, traitsFor)
	}
	for _, member := range node.Members {
		StampDescent(member, traitsFor)
	}
}

// DescentFromTraits builds a #DescentDescriptor from a substrate's DECLARED #DeployTraits,
// deriving the closed nesting-transport vocabulary GENERICALLY from the traits — never by
// switching on a substrate kind word. It copies the declared traits verbatim (the consult
// sites read them off node.Descent) and computes transport + host_rooted:
//
//	leaf_only            → reject          (kubernetes: a deploy-chain leaf, unreachable via exec)
//	venue == container   → container-exec  (pod: podman/docker exec by name)
//	venue == ssh         → ssh             (vm: an ssh hop into the guest)
//	otherwise            → none            (shell/parent/none share the parent venue)
//
// nil traits (a targetless group / empty target / a word with no declared substrate traits)
// map to the external-in-place default (container-exec) — behaviour-preserving for the former
// descentForTarget default arm that covered pod and the empty/group target.
func DescentFromTraits(t *DeployTraits) *DescentDescriptor {
	d := &DescentDescriptor{}
	if t != nil {
		d.Venue = t.Venue
		d.ImageBacked = t.ImageBacked
		d.ImageContext = t.ImageContext
		d.MachineVenue = t.MachineVenue
		d.ExclusiveVenue = t.ExclusiveVenue
		d.LeafOnly = t.LeafOnly
		d.BracketedLifecycle = t.BracketedLifecycle
	}
	switch {
	case t == nil:
		d.Transport = "container-exec"
	case t.LeafOnly:
		d.Transport = "reject"
	case t.Venue == "container":
		d.Transport = "container-exec"
	case t.Venue == "ssh":
		d.Transport = "ssh"
	default:
		d.Transport = "none"
	}
	// host_rooted: the substrate's own ROOT executor runs directly on the host (local:
	// venue==shell, not a leaf). kubernetes (shell + leaf_only) is a chain leaf, never descended,
	// so its host_rooted is irrelevant and left false.
	d.HostRooted = t != nil && t.Venue == "shell" && !t.LeafOnly
	return d
}
