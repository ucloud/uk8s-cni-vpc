package main

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/ryanuber/columnize"
	"github.com/spf13/cobra"
	ipamdv1beta1 "github.com/ucloud/uk8s-cni-vpc/apis/ipamd/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var Status = &cobra.Command{
	Use:   "status",
	Short: "Show ipamd status",

	RunE: func(_ *cobra.Command, _ []string) error {
		ctx := context.Background()
		ipamdList, err := crdClient.IpamdV1beta1().Ipamds("kube-system").List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list resources: %v", err)
		}

		if len(ipamdList.Items) == 0 {
			fmt.Println("no resources")
			return nil
		}

		lines := []string{"NAME | SIZE | STATUS"}
		for _, ipamd := range ipamdList.Items {
			var attr color.Attribute
			switch ipamd.Status.Status {
			case ipamdv1beta1.StatusNormal:
				attr = color.FgGreen

			case ipamdv1beta1.StatusDry:
				attr = color.FgRed

			case ipamdv1beta1.StatusHungry:
				attr = color.FgYellow

			case ipamdv1beta1.StatusOverflow:
				attr = color.FgMagenta
			}

			c := color.New(attr)
			line := fmt.Sprintf("%s | %d | %s", ipamd.Name, ipamd.Status.Current,
				c.Sprint(ipamd.Status.Status))
			lines = append(lines, line)
		}

		fmt.Println(columnize.SimpleFormat(lines))
		return nil
	},
}
