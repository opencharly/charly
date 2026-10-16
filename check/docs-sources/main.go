// docs-sources proves the committed documentation source configuration against
// the real generator and consumer corpus. It retains its inputs and outputs.
package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", ".", "committed charly checkout")
	generator := flag.String("generator", "", "pinned standalone plugin-docs binary")
	site := flag.String("site", "", "docs consumer checkout with corpus manifest and marketplace")
	narrative := flag.String("narrative", "", "pinned umbrella narrative sources")
	flag.Parse()
	for name, value := range map[string]*string{"root": root, "generator": generator, "site": site, "narrative": narrative} {
		if *value == "" {
			return fmt.Errorf("--%s is required", name)
		}
		p, err := filepath.Abs(*value)
		if err != nil {
			return err
		}
		*value = p
	}
	if err := command(*root, nil, "git", "diff", "--quiet", "HEAD"); err != nil {
		return err
	}
	config, err := os.ReadFile(filepath.Join(*root, "charly.yml"))
	if err != nil {
		return err
	}
	for _, key := range []string{"release_repos", "extra_repos"} {
		pattern := regexp.MustCompile(`(?m)^\s+` + key + `: \[\]\s*$`)
		if !pattern.Match(config) {
			return fmt.Errorf("%s must be empty; bare source names resolve moving tags", key)
		}
	}
	wiring, err := os.ReadFile(filepath.Join(*root, "candy/docs-sources/charly.yml"))
	if err != nil {
		return err
	}
	refs := regexp.MustCompile(`@github\.com/opencharly/(plugin-[a-z]+)/candy/[^:]+:(v[0-9.]+)`).FindAllStringSubmatch(string(wiring), -1)
	if len(refs) != 4 {
		return fmt.Errorf("expected four explicit documentation refs, got %d", len(refs))
	}
	want := map[string]bool{"plugin-review": true, "plugin-pipeline": true, "plugin-gh": true, "plugin-docs": true}
	for _, ref := range refs {
		if !want[ref[1]] {
			return fmt.Errorf("unexpected or duplicated source %s", ref[1])
		}
		delete(want, ref[1])
	}
	if len(want) != 0 {
		return fmt.Errorf("missing documentation sources: %v", want)
	}
	status, err := exec.Command("git", "-C", *root, "submodule", "status", "--recursive").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(status), "\n"), "\n") {
		if line != "" && line[0] != ' ' {
			return fmt.Errorf("submodule not at recorded pin: %s", line)
		}
	}
	head, err := exec.Command("git", "-C", *root, "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(*root, ".check"), 0755); err != nil {
		return err
	}
	proof, err := os.MkdirTemp(filepath.Join(*root, ".check"), "docs-sources-")
	if err != nil {
		return err
	}
	fmt.Printf("source head: %sproof: %s\n", head, proof)
	// Snapshot the consumer once: another author may edit its checkout during
	// a long cold-cache generation, but both runs must have identical inputs.
	consumer := filepath.Join(proof, "consumer")
	for _, path := range []string{"astro.config.mjs", ".github/docs-corpus/build_wiring.py", ".github/docs-corpus/pins.tsv"} {
		if err := copyFile(filepath.Join(*site, path), filepath.Join(consumer, path)); err != nil {
			return err
		}
	}
	if err := copyTree(filepath.Join(*site, "src/content/docs"), filepath.Join(consumer, "src/content/docs")); err != nil {
		return err
	}
	inputs := filepath.Join(proof, "source")
	files, err := exec.Command("git", "-C", *root, "ls-files", "--recurse-submodules", "-z").Output()
	if err != nil {
		return err
	}
	for _, name := range bytes.Split(files, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		if err := copyFile(filepath.Join(*root, string(name)), filepath.Join(inputs, string(name))); err != nil {
			return err
		}
	}
	corpus := filepath.Join(inputs, "candy/docs-corpus/charly.yml")
	if err := os.MkdirAll(filepath.Dir(corpus), 0755); err != nil {
		return err
	}
	if err := command(consumer, nil, "python3", filepath.Join(consumer, ".github/docs-corpus/build_wiring.py"), filepath.Join(consumer, ".github/docs-corpus/pins.tsv"), corpus); err != nil {
		return err
	}
	var hashes []string
	for _, attempt := range []string{"first", "second"} {
		snapshot := filepath.Join(proof, attempt)
		if err := copyFile(filepath.Join(consumer, "astro.config.mjs"), filepath.Join(snapshot, "astro.config.mjs")); err != nil {
			return err
		}
		out := filepath.Join(snapshot, "src/content/docs")
		if err := copyTree(filepath.Join(consumer, "src/content/docs"), out); err != nil {
			return err
		}
		cache := "CHARLY_REPO_CACHE=" + filepath.Join(proof, attempt+"-cache")
		if err := command(inputs, []string{cache}, *generator, "generate", "--root", inputs, "--narrative", *narrative, "--plugins", filepath.Join(*site, "marketplace"), "--out", out); err != nil {
			return err
		}
		for _, ref := range refs {
			segment := strings.ReplaceAll("github-com-opencharly-"+ref[1]+"-"+ref[2], ".", "-")
			page := filepath.Join(out, "reference/plugin", segment, ref[1]+".md")
			if _, err := os.Stat(page); err != nil {
				return fmt.Errorf("pinned source page %s: %w", page, err)
			}
			fmt.Printf("%s source preserved: %s@%s\n", attempt, ref[1], ref[2])
		}
		hash, err := treeHash(out)
		if err != nil {
			return err
		}
		hashes = append(hashes, hash)
		fmt.Printf("%s content sha256: %s\n", attempt, hash)
	}
	if hashes[0] != hashes[1] {
		return fmt.Errorf("independent fresh-cache generation differs: %v", hashes)
	}
	out := filepath.Join(proof, "first/src/content/docs")
	if err := command(inputs, []string{"CHARLY_REPO_CACHE=" + filepath.Join(proof, "first-cache")}, *generator, "generate", "--root", inputs, "--narrative", *narrative, "--plugins", filepath.Join(*site, "marketplace"), "--out", out); err != nil {
		return err
	}
	hash, err := treeHash(out)
	if err != nil {
		return err
	}
	if hash != hashes[0] {
		return fmt.Errorf("regeneration changed content: %s != %s", hash, hashes[0])
	}
	fmt.Println("DOCS-SOURCES-PASS: four pinned sources preserved; fresh caches agree; regeneration is byte-identical")
	return nil
}

func command(dir string, env []string, name string, args ...string) error {
	fmt.Printf("$ %s%s %s\n", envPrefix(env), name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func envPrefix(env []string) string {
	if len(env) == 0 {
		return ""
	}
	return strings.Join(env, " ") + " "
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

func treeHash(root string) (string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			names = append(names, path)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00%x\n", rel, sha256.Sum256(data))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
