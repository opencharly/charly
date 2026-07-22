package kube

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// merge.go holds the k3s kubeconfig-merge moved out of charly/k3s_post.go. The
// clientcmd merge lives entirely in this plugin, out of charly's core go.mod —
// mergeKubeconfig is called directly by k3s_post.go's k3sPostProvision (S3, FINAL/K5
// unit 6), the WHOLE k3s post-provision finalization this plugin now owns
// end-to-end: charly's host-side K3sPostProvision (in `charly bundle add`)
// dispatches ONE synthetic `kube: k3s-post-provision` Op (via
// k8s_plugin.go's invokeKubePluginWithBroker) carrying the artifact key + deploy
// name; this plugin retrieves the cached kubeconfig, rewrites its guest-forwarded
// server, and merges it — no separate host-orchestrated merge round-trip.

// mergeKubeconfig reads the retrieved kubeconfig and merges it into the operator's
// ~/.kube/config under contextName. Existing entries with the same
// context/cluster/user name are OVERWRITTEN — deploy-add is the single source of
// truth for clusters it manages, so a rebuild cleanly picks up a fresh admin cert
// without stale entries. Returns a short human-readable success message.
func mergeKubeconfig(retrievedPath, contextName string) (string, error) {
	if retrievedPath == "" {
		return "", fmt.Errorf("no retrieved kubeconfig path given")
	}
	if contextName == "" {
		return "", fmt.Errorf("no context name given")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	kubeConfigPath := filepath.Join(home, ".kube", "config")

	// Load the retrieved kubeconfig.
	srcCfg, err := clientcmd.LoadFromFile(retrievedPath)
	if err != nil {
		return "", fmt.Errorf("loading retrieved kubeconfig %s: %w", retrievedPath, err)
	}
	if len(srcCfg.Contexts) == 0 {
		return "", fmt.Errorf("retrieved kubeconfig has no contexts")
	}

	// k3s emits kubeconfig with one context named "default" that points at the
	// "default" cluster + "default" user. Rename each of these to the deploy-named
	// context so multiple clusters can coexist in one ~/.kube/config. k3s always
	// emits exactly one of each.
	var srcCtxName string
	for n := range srcCfg.Contexts {
		srcCtxName = n
		break
	}
	srcCtx := srcCfg.Contexts[srcCtxName]
	srcClusterName := srcCtx.Cluster
	srcUserName := srcCtx.AuthInfo

	// Load (or initialize) the destination kubeconfig.
	var dstCfg *clientcmdapi.Config
	if _, err := os.Stat(kubeConfigPath); err == nil {
		dstCfg, err = clientcmd.LoadFromFile(kubeConfigPath)
		if err != nil {
			return "", fmt.Errorf("loading existing kubeconfig %s: %w", kubeConfigPath, err)
		}
	} else {
		dstCfg = clientcmdapi.NewConfig()
	}

	// Upsert with deploy-named keys.
	dstCfg.Clusters[contextName] = srcCfg.Clusters[srcClusterName]
	dstCfg.AuthInfos[contextName] = srcCfg.AuthInfos[srcUserName]
	newCtx := clientcmdapi.NewContext()
	newCtx.Cluster = contextName
	newCtx.AuthInfo = contextName
	if srcCtx.Namespace != "" {
		newCtx.Namespace = srcCtx.Namespace
	}
	dstCfg.Contexts[contextName] = newCtx
	// Leave CurrentContext alone — adding a cluster must not silently switch the
	// operator's default context.

	if err := os.MkdirAll(filepath.Dir(kubeConfigPath), 0o755); err != nil {
		return "", err
	}
	if err := clientcmd.WriteToFile(*dstCfg, kubeConfigPath); err != nil {
		return "", fmt.Errorf("writing %s: %w", kubeConfigPath, err)
	}
	return fmt.Sprintf("merged kubeconfig into %s under context %q", kubeConfigPath, contextName), nil
}
