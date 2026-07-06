package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScaffoldCandy creates a new candy directory with placeholder files
func ScaffoldCandy(dir string, name string) error {
	candyDir := filepath.Join(dir, DefaultCandyDir, name)

	// Check if candy already exists
	if _, err := os.Stat(candyDir); err == nil {
		return fmt.Errorf("candy %q already exists at %s", name, candyDir)
	}

	// Create candy directory
	if err := os.MkdirAll(candyDir, 0755); err != nil {
		return fmt.Errorf("creating candy directory: %w", err)
	}

	// Create a placeholder candy manifest in the compact name-first node form,
	// named via the single configurable default (UnifiedFileName). ADE mandates a
	// description + at least one deterministic check: step, so the scaffold ships
	// a minimal passing pair the author replaces.
	candyYml := filepath.Join(candyDir, UnifiedFileName)
	candyContent := fmt.Sprintf(`# %s candy config
%s:
    candy:
        version: %s
        description: |
            TODO: one-line purpose of the %s candy
        # Add packages:  charly candy add-rpm %s <pkg>   (also add-deb / add-pac / add-aur)
        plan:
            - check: the /etc/os-release marker exists (replace with a real check)
              file: /etc/os-release
`, name, name, ComputeCalVer(), name, name)
	if err := os.WriteFile(candyYml, []byte(candyContent), 0644); err != nil {
		return fmt.Errorf("creating %s: %w", UnifiedFileName, err)
	}

	fmt.Printf("Created candy at %s\n", candyDir)
	fmt.Println("Files created:")
	fmt.Println("  charly.yml - Candy config (distro packages, require, env, port, route, service)")
	fmt.Println()
	fmt.Println("Optional files you can add:")
	fmt.Println("  root.yml        - Custom root install task")
	fmt.Println("  pixi.toml       - Python/conda packages")
	fmt.Println("  package.json    - npm packages")
	fmt.Println("  Cargo.toml      - Rust crate (requires src/)")
	fmt.Println("  user.yml        - Custom user install task")

	return nil
}
