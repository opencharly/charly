package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
)

// pixiBoundCandies identifies candies that have install files (user.yml/root.yml)
// but depend on a pixi environment from their including parent meta-candy.
// These candies must NOT be extracted into auto-intermediates because the
// intermediate won't have the pixi environment they need.
//
// A candy is pixi-bound if:
// 1. It has install files (user.yml or root.yml)
// 2. It does NOT have its own pixi manifest (pixi.toml/pyproject.toml/environment.yml)
// 3. It is included via candy: by another candy that DOES have a pixi manifest
func pixiBoundCandies(layers map[string]*Candy) map[string]bool {
	bound := make(map[string]bool)
	for _, layer := range layers {
		if layer.PixiManifest() == "" {
			continue
		}
		// This candy owns a pixi env. Check its IncludedCandies.
		for _, includedRef := range layer.IncludedCandy {
			included := includedRef.Bare()
			child, ok := layers[included]
			if !ok {
				continue
			}
			// If the included candy has install files but no pixi manifest,
			// it depends on this parent's pixi env and must not be extracted.
			if child.HasInstallFiles() && child.PixiManifest() == "" {
				bound[included] = true
			}
		}
	}
	return bound
}

// trieNode represents a node in the candy prefix trie.
type trieNode struct {
	layer    string               // layer at this position ("" for root)
	children map[string]*trieNode // layer name → child node
	boxes    []string             // user-defined images terminating here
}

func newTrieNode(layer string) *trieNode {
	return &trieNode{
		layer:    layer,
		children: make(map[string]*trieNode),
	}
}

// GlobalCandyOrder computes a global topological order of all candies across
// all enabled images, using popularity (number of images needing each candy)
// as the primary tie-breaker and lexicographic as secondary.
func GlobalCandyOrder(boxes map[string]*ResolvedBox, layers map[string]*Candy) ([]string, error) {
	// Count popularity: how many images need each candy (including transitive deps)
	popularity := make(map[string]int)
	for _, img := range boxes {
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			return nil, fmt.Errorf("resolving candies for image %q: %w", img.Name, err)
		}
		// Also include candies from the base chain
		allCandies := collectAllBoxCandies(img.Name, boxes, layers)
		// Merge resolved with allCandies
		seen := make(map[string]bool)
		for _, l := range allCandies {
			seen[l] = true
		}
		for _, l := range resolved {
			if !seen[l] {
				allCandies = append(allCandies, l)
				seen[l] = true
			}
		}
		for _, l := range allCandies {
			popularity[l]++
		}
	}

	// Build dependency graph from candy depends and included candies
	// Only include candies that appear in at least one image
	graph := make(map[string][]string)
	for name := range popularity {
		layer, ok := layers[name]
		if !ok {
			continue
		}
		var deps []string
		for _, depRef := range layer.Require {
			dep := depRef.Bare()
			if _, inUse := popularity[dep]; inUse {
				deps = append(deps, dep)
			}
		}
		for _, includedRef := range layer.IncludedCandy {
			included := includedRef.Bare()
			if _, inUse := popularity[included]; inUse {
				deps = append(deps, included)
			}
		}
		graph[name] = deps
	}

	// Authored candy-list order is an ordering CONSTRAINT, not just a seed set.
	// When an image (or metacandy) writes `candy: [A, B]`, the author means A's
	// install steps run before B's — even when B declares no `require: A`. The
	// canonical case is the builder images' `[rpmfusion, …, build-toolchain]`:
	// build-toolchain installs ffmpeg-devel / x264-devel / libva-devel, which
	// live in the RPM Fusion repos that the rpmfusion candy enables, yet
	// build-toolchain CANNOT `require: rpmfusion` (it is also used on Arch, where
	// those libs come from the distro repos). Without honoring authored order the
	// popularity tie-break can place build-toolchain ahead of rpmfusion in a
	// project whose image set makes build-toolchain the more popular candy,
	// emitting its dnf install before the repos exist and breaking the build.
	//
	// We add each list-adjacent graph-node pair as a dependency edge (the later
	// entry depends on the earlier), skipping any edge that is redundant or that
	// would create a cycle — so genuinely conflicting authored orders fall back
	// to the popularity tie-break exactly as before, while consistent orders
	// (the overwhelming majority) are now respected.
	isNode := func(name string) bool {
		if _, ok := popularity[name]; !ok {
			return false
		}
		_, ok := layers[name]
		return ok
	}
	addListOrderEdge := func(prev, cur string) {
		if prev == cur || !isNode(prev) || !isNode(cur) {
			return
		}
		if slices.Contains(graph[cur], prev) {
			return // already constrained
		}
		// Adding "cur depends on prev" creates a cycle iff prev already
		// (transitively) depends on cur.
		if graphReaches(graph, prev, cur) {
			return
		}
		graph[cur] = append(graph[cur], prev)
	}
	addListEdges := func(list []string) {
		for i := 1; i < len(list); i++ {
			addListOrderEdge(list[i-1], list[i])
		}
	}
	// Every authored candy list contributes ordering edges: image-level lists
	// AND metacandy `candy:` (IncludedCandy) lists. Non-node entries (pure-
	// composition metacandies with no RUN steps) are skipped by addListOrderEdge,
	// so only content candies are constrained.
	//
	// Iterate in sorted name order, NOT map-iteration order: addListOrderEdge
	// skips any edge that would create a cycle, so the SET of edges added depends
	// on insertion order. Go map iteration is randomized, which would make the
	// graph — and thus the global candy order and every auto-intermediate derived
	// from it — vary run-to-run, breaking cross-run cache reuse. Sorted iteration
	// makes the edge set, the topo order, and the intermediates deterministic.
	boxNames := make([]string, 0, len(boxes))
	for name := range boxes {
		boxNames = append(boxNames, name)
	}
	sortStrings(boxNames)
	for _, name := range boxNames {
		addListEdges(boxes[name].Candy)
	}
	candyNames := make([]string, 0, len(popularity))
	for name := range popularity {
		candyNames = append(candyNames, name)
	}
	sortStrings(candyNames)
	for _, name := range candyNames {
		if l, ok := layers[name]; ok {
			addListEdges(bareRefs(l.IncludedCandy))
		}
	}

	// Kahn's algorithm with popularity-based tie-breaking
	return topoSortByPopularity(graph, popularity)
}

// graphReaches reports whether `to` is reachable from `from` by following
// dependency edges (graph[x] lists the candies x depends on). Used to keep
// authored-list-order edge insertion cycle-safe in GlobalCandyOrder.
func graphReaches(graph map[string][]string, from, to string) bool {
	if from == to {
		return true
	}
	visited := make(map[string]bool)
	var dfs func(n string) bool
	dfs = func(n string) bool {
		if n == to {
			return true
		}
		if visited[n] {
			return false
		}
		visited[n] = true
		return slices.ContainsFunc(graph[n], dfs)
	}
	return dfs(from)
}

// topoSortByPopularity performs topological sort with popularity tie-breaking.
// Higher popularity candies come first among zero-in-degree candidates.
func topoSortByPopularity(graph map[string][]string, popularity map[string]int) ([]string, error) {
	inDegree := make(map[string]int)
	reverseGraph := make(map[string][]string)

	for node := range graph {
		inDegree[node] = len(graph[node])
		for _, dep := range graph[node] {
			reverseGraph[dep] = append(reverseGraph[dep], node)
		}
	}

	// Find all nodes with no dependencies
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}
	sortByPopularity(queue, popularity)

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		dependents := reverseGraph[node]
		for _, dep := range dependents {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
		sortByPopularity(queue, popularity)
	}

	if len(result) != len(graph) {
		return nil, fmt.Errorf("cycle detected in candy dependency graph")
	}
	return result, nil
}

// sortByPopularity sorts by descending popularity, then lexicographic ascending.
func sortByPopularity(s []string, popularity map[string]int) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			pi, pj := popularity[s[i]], popularity[s[j]]
			if pi < pj || (pi == pj && s[i] > s[j]) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// collectAllBoxCandies returns the complete set of candies for an image,
// including all candies inherited through the base chain.
func collectAllBoxCandies(boxName string, boxes map[string]*ResolvedBox, layers map[string]*Candy) []string {
	seen := make(map[string]bool)
	// walked is an IMAGE-visited guard for the base-chain recursion below. A
	// base edge may form a cycle (A.base=B, B.base=A); that's caught + reported
	// by ResolveBoxOrder, but without this guard the walk recurses a cyclic
	// chain until the stack overflows. Re-visiting a base also can't add new
	// candies (its candies were collected on the first visit), so skipping it is
	// correct for the acyclic case too.
	walked := make(map[string]bool)
	var result []string

	var walk func(name string)
	walk = func(name string) {
		if walked[name] {
			return
		}
		walked[name] = true
		img, ok := boxes[name]
		if !ok {
			return
		}
		if !img.IsExternalBase {
			walk(img.Base)
		}
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			return
		}
		for _, l := range resolved {
			if !seen[l] {
				seen[l] = true
				result = append(result, l)
			}
		}
	}
	walk(boxName)
	return result
}

// AbsoluteCandySequence returns an image's complete candy set (own + entire
// base chain) as a subsequence of the global order.
func AbsoluteCandySequence(boxName string, boxes map[string]*ResolvedBox, layers map[string]*Candy, globalOrder []string) []string {
	allCandies := collectAllBoxCandies(boxName, boxes, layers)
	candySet := make(map[string]bool, len(allCandies))
	for _, l := range allCandies {
		candySet[l] = true
	}

	// Filter global order to only include this image's candies
	var seq []string
	for _, l := range globalOrder {
		if candySet[l] {
			seq = append(seq, l)
		}
	}
	return seq
}

// siblingKey identifies a sibling-grouping equivalence class for intermediate
// computation. Grouping by (Base, UID) — not just Base — ensures that images
// with different user contexts (e.g. uid=1000 default vs. uid=0 root) don't
// share an auto-intermediate. Sharing would bake one group's HOME-relative
// paths (path_append `~/.foo/bin`, env vars using `~` or `$HOME`) into the
// intermediate's `ENV PATH` directives, leaving the other group with dead
// PATH entries that can't be overridden by its own ENV emission.
type siblingKey struct {
	base string
	uid  int
}

// ComputeIntermediates analyzes all images, groups them by (Base, UID),
// builds prefix tries of relative candy sequences within each sibling group,
// creates intermediates at branching points, and returns updated images map.
// User-defined images always take priority over auto-intermediates.
func ComputeIntermediates(boxes map[string]*ResolvedBox, layers map[string]*Candy, cfg *Config, tag string) (map[string]*ResolvedBox, error) {
	globalOrder, err := GlobalCandyOrder(boxes, layers)
	if err != nil {
		return nil, fmt.Errorf("computing global candy order: %w", err)
	}

	// Copy all existing images
	result := make(map[string]*ResolvedBox)
	for name, img := range boxes {
		cp := *img
		result[name] = &cp
	}

	// Compute pixi-bound candies: these must not be extracted into intermediates
	pixiBound := pixiBoundCandies(layers)

	// Collect all builder image names to exclude from intermediate generation.
	builderNames := make(map[string]bool)
	for _, builder := range cfg.Defaults.Builder {
		if builder != "" {
			builderNames[builder] = true
		}
	}
	// Also exclude builders referenced by ANY image's builder map (not just
	// defaults) — e.g. a submodule consumer's `builder: {pixi: charly.arch-builder}`.
	// Without this, a pulled namespaced builder (charly.arch-builder) would be grouped
	// with its consumers and factored into an intermediate it must itself build,
	// producing a `builder -> intermediate -> builder` dependency cycle.
	for _, img := range boxes {
		for _, builder := range img.Builder {
			if builder != "" {
				builderNames[builder] = true
			}
		}
	}

	// Default UID — used to decide whether an intermediate needs a UID
	// suffix in its name to avoid collision with the default-UID sibling.
	defaultUID := resolveIntPtr(cfg.Defaults.UID, nil, 1000)

	// Group images by (Base, UID). See siblingKey docstring for rationale.
	siblingGroups := make(map[siblingKey][]string)
	for name, img := range boxes {
		if builderNames[name] {
			continue
		}
		// Pulled namespace-qualified images (e.g. charly.arch, charly.arch-builder,
		// cachyos.cachyos) are external/fixed dependencies, not local siblings —
		// never factor them into local intermediates. (Local consumers that root
		// ON them have unqualified names and ARE grouped by their qualified base.)
		if strings.Contains(name, ".") {
			continue
		}
		k := siblingKey{img.Base, img.UID}
		siblingGroups[k] = append(siblingGroups[k], name)
	}

	// Process internal-base groups in topological order (parents before children)
	// so auto-intermediates from parent groups are visible when processing child groups
	imageOrder, err := ResolveBoxOrder(boxes, layers)
	if err != nil {
		return nil, fmt.Errorf("resolving box order: %w", err)
	}

	// Sibling-group processing order MUST be deterministic: createIntermediate
	// writes into `result`, and pickAutoName's collision suffix (`-2`/`-3`) is
	// assigned by checking already-created names — so the order groups are
	// processed decides which group claims the bare name and which gets the
	// suffix. Map iteration is randomized, so iterate keys sorted by (base, uid)
	// instead, keeping intermediate names stable run-to-run AND identical whether
	// one box or all are generated (ComputeIntermediates always runs over the
	// full set, so the only variable was iteration order).
	orderedKeys := sortedSiblingKeys(siblingGroups)

	processed := make(map[siblingKey]bool)
	for _, parentName := range imageOrder {
		// Each parent may host multiple sibling groups (one per UID).
		// Iterate every sibling key whose base matches parentName.
		for _, k := range orderedKeys {
			if k.base != parentName || len(siblingGroups[k]) < 2 {
				continue
			}
			processed[k] = true
			if err := processSiblingGroup(k.base, k.uid, defaultUID, siblingGroups[k], result, boxes, layers, cfg, tag, globalOrder, pixiBound); err != nil {
				return nil, err
			}
		}
	}

	// Process external-base groups (parent is an external OCI ref, not in imageOrder)
	for _, k := range orderedKeys {
		if processed[k] || len(siblingGroups[k]) < 2 {
			continue
		}
		if err := processSiblingGroup(k.base, k.uid, defaultUID, siblingGroups[k], result, boxes, layers, cfg, tag, globalOrder, pixiBound); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// sortedSiblingKeys returns the sibling-group keys in a deterministic order —
// by base name, then UID. Used to make auto-intermediate creation independent of
// Go's randomized map iteration (see ComputeIntermediates).
func sortedSiblingKeys(m map[siblingKey][]string) []siblingKey {
	keys := make([]siblingKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].base != keys[j].base {
			return keys[i].base < keys[j].base
		}
		return keys[i].uid < keys[j].uid
	})
	return keys
}

// processSiblingGroup builds a prefix trie from the relative candy sequences
// of children sharing the same parent + uid, and creates intermediates at
// branch points. The uid is the shared UID of this sibling group; it flows
// through walkTrieScoped into createIntermediate so the emitted ENV PATH
// references the correct HOME for this group's user context.
func processSiblingGroup(parentName string, uid, defaultUID int, children []string, result, origBoxes map[string]*ResolvedBox, layers map[string]*Candy, cfg *Config, tag string, globalOrder []string, pixiBound map[string]bool) error {
	sortStrings(children)

	// Get candies provided by parent
	parentProvided := make(map[string]bool)
	if _, ok := result[parentName]; ok {
		provided, err := CandyProvidedByBox(parentName, result, layers)
		if err == nil {
			parentProvided = provided
		}
	}

	// Build trie from relative candy sequences
	root := newTrieNode("")
	for _, childName := range children {
		seq := relativeCandySequence(childName, parentProvided, result, layers, globalOrder, pixiBound)
		node := root
		for _, layer := range seq {
			child, ok := node.children[layer]
			if !ok {
				child = newTrieNode(layer)
				node.children[layer] = child
			}
			node = child
		}
		node.boxes = append(node.boxes, childName)
	}

	return walkTrieScoped(root, parentName, uid, defaultUID, result, origBoxes, layers, cfg, tag, globalOrder, pixiBound)
}

// relativeCandySequence returns an image's candies minus what the parent provides,
// ordered according to the global candy order.
func relativeCandySequence(boxName string, parentProvided map[string]bool, boxes map[string]*ResolvedBox, layers map[string]*Candy, globalOrder []string, pixiBound map[string]bool) []string {
	allCandies := collectAllBoxCandies(boxName, boxes, layers)
	candySet := make(map[string]bool, len(allCandies))
	for _, l := range allCandies {
		candySet[l] = true
	}

	var seq []string
	for _, l := range globalOrder {
		if candySet[l] && !parentProvided[l] && !pixiBound[l] {
			seq = append(seq, l)
		}
	}
	return seq
}

// walkTrieScoped walks the trie creating intermediates at branch points.
// User-defined images at branch points are reused as intermediates without rebasing.
// uid + defaultUID propagate from the sibling group so auto-intermediates
// inherit the right user context and get UID-suffixed names when needed.
func walkTrieScoped(node *trieNode, parentName string, uid, defaultUID int, result map[string]*ResolvedBox, origBoxes map[string]*ResolvedBox, layers map[string]*Candy, cfg *Config, tag string, globalOrder []string, pixiBound map[string]bool) error {
	for _, childCandyName := range sortedKeys(node.children) {
		child := node.children[childCandyName]

		// Collect linear chain: walk as long as exactly one child and no terminal images
		var pathCandies []string
		current := child
		pathCandies = append(pathCandies, childCandyName)

		for len(current.children) == 1 && len(current.boxes) == 0 {
			for candyName, next := range current.children {
				pathCandies = append(pathCandies, candyName)
				current = next
			}
		}

		// current is at a branch point, leaf, or has terminal images
		isBranch := len(current.children) >= 2 || (len(current.children) >= 1 && len(current.boxes) > 0)
		isLeaf := len(current.children) == 0

		if isBranch {
			// Count user-defined images at this branch point
			var userBoxes []string
			for _, img := range current.boxes {
				if _, isOrig := origBoxes[img]; isOrig {
					userBoxes = append(userBoxes, img)
				}
			}

			if len(userBoxes) == 1 && len(current.boxes) == 1 {
				// Single user image at branch: use it as intermediate, preserve its Base
				intermediateName := userBoxes[0]
				if err := walkTrieScoped(current, intermediateName, uid, defaultUID, result, origBoxes, layers, cfg, tag, globalOrder, pixiBound); err != nil {
					return err
				}
			} else {
				// 0 or 2+ user images: create auto-intermediate
				intermediateName := pickAutoName(pathCandies, parentName, uid, defaultUID, result, origBoxes)
				// Every terminal image in this subtree will base (directly or
				// transitively) on this intermediate, so it must carry the UNION
				// of their build formats / distro tags — a candy hoisted here whose
				// package section is keyed on a format only the consumers declare
				// would otherwise be silently dropped. See createIntermediate.
				consumerBoxes := collectSubtreeBoxes(current)
				createIntermediate(intermediateName, parentName, uid, pathCandies, consumerBoxes, result, origBoxes, cfg, tag, layers, globalOrder, pixiBound)
				// Rebase all terminal images to this intermediate
				for _, imgName := range current.boxes {
					updateBoxBase(imgName, intermediateName, result)
				}
				if err := walkTrieScoped(current, intermediateName, uid, defaultUID, result, origBoxes, layers, cfg, tag, globalOrder, pixiBound); err != nil {
					return err
				}
			}
		} else if isLeaf {
			// Terminal images at leaf — rebase to current parent
			for _, imgName := range current.boxes {
				updateBoxBase(imgName, parentName, result)
			}
		}
	}
	return nil
}

// collectSubtreeBoxes returns every terminal user image in the subtree rooted
// at node — the images terminating at node plus all images in descendant nodes.
// These are exactly the images that will base, directly or transitively, on an
// auto-intermediate created at this node, so they define the union of build
// formats / distro tags the intermediate must carry (see createIntermediate).
func collectSubtreeBoxes(node *trieNode) []string {
	out := append([]string(nil), node.boxes...)
	// Sorted child traversal (not map order) so consumerBoxes — and the
	// format/distro union createIntermediate derives from it — are deterministic.
	for _, childName := range sortedKeys(node.children) {
		out = append(out, collectSubtreeBoxes(node.children[childName])...)
	}
	return out
}

// pickAutoName chooses a name for an auto-intermediate using {parent}-{lastCandy}.
// For OCI refs (e.g. "quay.io/fedora/fedora:43"), extracts the short image name.
// When uid != defaultUID, appends "-uid<N>" so uid=0 and uid=1000 intermediates
// at the same trie position get distinct OCI tags (otherwise they'd collide
// and one group's HOME-baked ENV would poison the other).
// Appends -2, -3 etc. to avoid conflicts with existing or already-created images.
func pickAutoName(pathCandies []string, parentName string, uid, defaultUID int, result, origBoxes map[string]*ResolvedBox) string {
	lastCandy := pathCandies[len(pathCandies)-1]
	// Remote candy keys are fully-qualified paths
	// ("github.com/opencharly/charly/candy/pixi"); reduce to the short
	// candy name so the intermediate gets a valid, slash-free OCI image name
	// ("arch-pixi", not "arch-github.com/.../candy/pixi" — the latter is a
	// malformed ref that crashes buildah's content-summary on COPY/FROM). Local
	// candy keys are already short, so this is a no-op for them.
	if i := strings.LastIndex(lastCandy, "/"); i >= 0 {
		lastCandy = lastCandy[i+1:]
	}

	// Extract short parent name from OCI refs: "quay.io/fedora/fedora:43" → "fedora"
	shortParent := parentName
	if i := strings.LastIndex(shortParent, ":"); i >= 0 {
		shortParent = shortParent[:i]
	}
	if i := strings.LastIndex(shortParent, "/"); i >= 0 {
		shortParent = shortParent[i+1:]
	}

	baseName := shortParent + "-" + lastCandy
	if uid != defaultUID {
		baseName = fmt.Sprintf("%s-uid%d", baseName, uid)
	}
	name := baseName
	suffix := 2
	for {
		if _, exists := origBoxes[name]; !exists {
			if _, exists := result[name]; !exists {
				return name
			}
		}
		name = fmt.Sprintf("%s-%d", baseName, suffix)
		suffix++
	}
}

// createIntermediate creates an auto-generated intermediate image in the result map.
// uid is the sibling group's UID — it determines the intermediate's User/GID/Home
// so HOME-relative env/path_append expansion matches the children that will
// inherit from this intermediate.
func createIntermediate(name, parentName string, uid int, pathCandies []string, consumerBoxes []string, result map[string]*ResolvedBox, origBoxes map[string]*ResolvedBox, cfg *Config, tag string, layers map[string]*Candy, globalOrder []string, pixiBound map[string]bool) {
	ownCandies := computeOwnCandies(parentName, pathCandies, result, layers, globalOrder, pixiBound)

	isExternalBase := false
	if _, ok := result[parentName]; !ok {
		isExternalBase = true
	}

	platforms := resolvePlatforms(cfg)
	if parent, ok := result[parentName]; ok && len(parent.Platforms) > 0 {
		platforms = intersectPlatforms(parent.Platforms, platforms)
	}

	// Distro + BuildFormats MUST come from the parent first. Only non-auto
	// parents have their own values; external roots fall back to defaults.
	// This was previously inverted — defaults won even when the parent was
	// an explicit non-default base (e.g. arch with build: [pac]), so
	// every arch-rooted auto-intermediate got mis-tagged as build: [rpm]
	// and all `pac:`-only candy sections silently dropped out of the
	// generated Containerfile. Fixed by resolving parent first.
	var inheritedDistro []string
	var inheritedBuilds []string
	if parent, ok := result[parentName]; ok {
		inheritedDistro = parent.Distro
		inheritedBuilds = parent.BuildFormats
	}
	if len(inheritedDistro) == 0 {
		inheritedDistro = cfg.Defaults.Distro
	}
	if len(inheritedBuilds) == 0 {
		inheritedBuilds = cfg.Defaults.Build
	}

	// An auto-intermediate hosts candies hoisted out of its consuming images.
	// When a hoisted candy's package section is keyed on a build format (or
	// distro tag) the PARENT chain doesn't declare but a CONSUMER does — e.g.
	// the cachyos base is build:[pac] while selkies-labwc/openclaw-desktop are
	// build:[pac,aur] and the hoisted chrome candy needs aur for google-chrome —
	// parent-only inheritance silently drops that section (the AUR gate in
	// generate.go keys on BuildFormats). Union the parent's formats/distro with
	// every consuming descendant's, keeping the parent's primary format FIRST
	// (it drives img.Pkg + cache mounts below). No-op when consumers share the
	// parent's formats (the common case). Mirrors the parent-first inheritance
	// fix above for the orthogonal "format declared on children" case.
	buildSeen := make(map[string]bool, len(inheritedBuilds))
	for _, f := range inheritedBuilds {
		buildSeen[f] = true
	}
	distroSeen := make(map[string]bool, len(inheritedDistro))
	for _, d := range inheritedDistro {
		distroSeen[d] = true
	}
	for _, cname := range consumerBoxes {
		c, ok := result[cname]
		if !ok {
			c, ok = origBoxes[cname]
		}
		if !ok {
			continue
		}
		for _, f := range c.BuildFormats {
			if !buildSeen[f] {
				buildSeen[f] = true
				inheritedBuilds = append(inheritedBuilds, f)
			}
		}
		for _, d := range c.Distro {
			if !distroSeen[d] {
				distroSeen[d] = true
				inheritedDistro = append(inheritedDistro, d)
			}
		}
	}

	// Derive User/GID/Home from the sibling group's UID. uid=0 is root with
	// /root as HOME; any other UID reuses cfg.Defaults.User (typically "user")
	// and /home/<user>. This keeps HOME-relative ENV expansion consistent
	// with the child images that inherit this intermediate.
	var user string
	var gid int
	if uid == 0 {
		user = "root"
		gid = 0
	} else {
		user = cfg.Defaults.User
		if user == "" {
			user = "user"
		}
		gid = resolveIntPtr(cfg.Defaults.GID, nil, 1000)
	}

	// Builder map: defaults as the base, then the PARENT, then the CONSUMERS win.
	// The hoisted candies belong to the consumers, so the consumers' builder map is
	// authoritative for them. In the flat case the consumers inherit the parent's
	// builder (so they agree — consumer-wins is a no-op vs parent-wins). In the
	// import-namespace case the parent is a cross-namespace base (e.g.
	// cachyos.cachyos) whose builder refs are relative to ITS namespace
	// (`charly.arch-builder`) and do NOT resolve in this context; the consumers carry
	// the correct context-local builder (`arch-builder`), so consumer-wins is what
	// lets the hoisted AUR candy (chrome's google-chrome) find its builder instead
	// of failing with "needs builder aur but no builders.aur configured".
	builderMap := make(BuilderMap)
	maps.Copy(builderMap, cfg.Defaults.Builder)
	// Distro-keyed default — the SAME mechanism ResolveBox /
	// resolveEffectiveBuilder use: a cachyos/Arch intermediate seeds
	// arch-builder from the root-namespace distro image, so it never falls back
	// to the Fedora default even before the consumer-wins merge below (which
	// remains authoritative for the hoisted candies).
	maps.Copy(builderMap, cfg.distroBuilderMap(inheritedDistro))
	if parent, ok := result[parentName]; ok {
		maps.Copy(builderMap, parent.Builder)
	}
	for _, cname := range consumerBoxes {
		c, ok := result[cname]
		if !ok {
			c, ok = origBoxes[cname]
		}
		if !ok {
			continue
		}
		maps.Copy(builderMap, c.Builder)
	}

	img := &ResolvedBox{
		Name:           name,
		Base:           parentName,
		IsExternalBase: isExternalBase,
		Candy:          ownCandies,
		Tag:            tag,
		Registry:       cfg.Defaults.Registry,
		Distro:         inheritedDistro,
		BuildFormats:   inheritedBuilds,
		Platforms:      platforms,
		User:           user,
		UID:            uid,
		GID:            gid,
		Merge:          cfg.Defaults.Merge,
		Builder:        builderMap,
		Auto:           true,
	}
	if len(img.BuildFormats) == 0 {
		fmt.Fprintf(os.Stderr, "Warning: auto-intermediate %s has no build formats (set build: in defaults)\n", name)
		return
	}
	img.Pkg = img.BuildFormats[0]
	// Inherit format configs from parent image (auto-intermediates share the same configs)
	if parent, ok := result[parentName]; ok {
		img.DistroConfig = parent.DistroConfig
		img.DistroDef = parent.DistroDef
		img.BuilderConfig = parent.BuilderConfig
	}
	// Build unified Tags: ["all"] + Distro + BuildFormats
	img.Tags = append([]string{"all"}, img.Distro...)
	img.Tags = append(img.Tags, img.BuildFormats...)
	if img.User == "root" {
		img.Home = "/root"
	} else {
		img.Home = fmt.Sprintf("/home/%s", img.User)
	}
	if img.Registry != "" {
		img.FullTag = fmt.Sprintf("%s/%s:%s", img.Registry, name, tag)
	} else {
		img.FullTag = fmt.Sprintf("%s:%s", name, tag)
	}

	result[name] = img
}

// computeOwnCandies determines which candies an intermediate needs to install
// (pathCandies minus what the parent already provides).
func computeOwnCandies(parentName string, pathCandies []string, result map[string]*ResolvedBox, layers map[string]*Candy, globalOrder []string, pixiBound map[string]bool) []string {
	parentProvided := make(map[string]bool)
	if _, ok := result[parentName]; ok {
		provided, err := CandyProvidedByBox(parentName, result, layers)
		if err == nil {
			parentProvided = provided
		}
	}

	var own []string
	for _, l := range pathCandies {
		if !parentProvided[l] {
			own = append(own, l)
		}
	}

	// Also include transitive dependencies of these candies that aren't parent-provided
	needed := make(map[string]bool)
	for _, l := range own {
		needed[l] = true
		addTransitiveDeps(l, layers, needed, parentProvided)
	}

	// Return in global order, excluding pixi-bound candies
	var ordered []string
	for _, l := range globalOrder {
		if needed[l] && !parentProvided[l] && !pixiBound[l] {
			ordered = append(ordered, l)
		}
	}
	if len(ordered) == 0 {
		return own // fallback
	}
	return ordered
}

// addTransitiveDeps adds all transitive dependencies of a candy to the needed set.
func addTransitiveDeps(candyName string, layers map[string]*Candy, needed map[string]bool, excluded map[string]bool) {
	layer, ok := layers[candyName]
	if !ok {
		return
	}
	for _, depRef := range layer.Require {
		dep := depRef.Bare()
		if excluded[dep] || needed[dep] {
			continue
		}
		needed[dep] = true
		addTransitiveDeps(dep, layers, needed, excluded)
	}
	for _, includedRef := range layer.IncludedCandy {
		included := includedRef.Bare()
		if excluded[included] || needed[included] {
			continue
		}
		needed[included] = true
		addTransitiveDeps(included, layers, needed, excluded)
	}
}

// updateBoxBase updates an image's Base to point to the given parent.
func updateBoxBase(imgName, parentName string, result map[string]*ResolvedBox) {
	img, ok := result[imgName]
	if !ok {
		return
	}
	img.Base = parentName
	if _, isInternal := result[parentName]; isInternal {
		img.IsExternalBase = false
	} else {
		img.IsExternalBase = true
	}
}

// resolvePlatforms returns platforms from config defaults.
func resolvePlatforms(cfg *Config) []string {
	if len(cfg.Defaults.Platforms) > 0 {
		return cfg.Defaults.Platforms
	}
	return []string{"linux/amd64", "linux/arm64"}
}

// intersectPlatforms returns platforms present in both slices.
// If the intersection is empty, returns parent (the more restrictive set).
func intersectPlatforms(parent, defaults []string) []string {
	set := make(map[string]bool, len(parent))
	for _, p := range parent {
		set[p] = true
	}
	var result []string
	for _, p := range defaults {
		if set[p] {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return parent
	}
	return result
}

// sortedKeys returns sorted keys from a map.
func sortedKeys(m map[string]*trieNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}
