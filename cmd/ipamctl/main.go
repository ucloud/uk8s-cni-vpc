package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	crdclientset "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/clientset/versioned"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	configPath string

	crdClient *crdclientset.Clientset
)

var Root = &cobra.Command{
	Use:   "ipamctl [--kube-config config-path]",
	Short: "Ipam CLI tool",

	SilenceErrors: true,
	SilenceUsage:  true,

	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		if configPath == "" {
			homePath, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			configPath = filepath.Join(homePath, ".kube", "config")
		}

		cfg, err := clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			return fmt.Errorf("failed to read kube-config: %v", err)
		}

		crdClient, err = crdclientset.NewForConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to init client: %v", err)
		}

		return nil
	},

	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
}

func main() {
	Root.Flags().StringVarP(&configPath, "kube-config", "", "", "kubeconfig path")

	Root.AddCommand(Status)

	err := Root.Execute()
	if err != nil {
		fmt.Printf("%s %v\n", color.RedString("error:"), err)
		os.Exit(1)
	}
}
