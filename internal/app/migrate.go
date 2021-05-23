package app

import (
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/utkuozdemir/pv-migrate/engine"
	"github.com/utkuozdemir/pv-migrate/internal/strategy"
	"github.com/utkuozdemir/pv-migrate/migration"
)

const (
	CommandMigrate = "migrate"
)

func buildMigrateCmd() *cobra.Command {
	cmd := cobra.Command{
		Use:     CommandMigrate,
		Aliases: []string{"m"},
		Short:   "Migrate data from one Kubernetes PersistentVolumeClaim to another",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			sourceKubeconfig, _ := f.GetString(FlagSourceKubeconfig)
			sourceContext, _ := f.GetString(FlagSourceContext)
			sourceNamespace, _ := f.GetString(FlagSourceNamespace)
			s := migration.PVC{
				KubeconfigPath: sourceKubeconfig,
				Context:        sourceContext,
				Namespace:      sourceNamespace,
				Name:           args[0],
			}

			destKubeconfig, _ := f.GetString(FlagDestKubeconfig)
			destContext, _ := f.GetString(FlagDestContext)
			destNamespace, _ := f.GetString(FlagDestNamespace)
			d := migration.PVC{
				KubeconfigPath: destKubeconfig,
				Context:        destContext,
				Namespace:      destNamespace,
				Name:           args[1],
			}

			destDeleteExtraneousFiles, _ := f.GetBool(FlagDestDeleteExtraneousFiles)
			ignoreMounted, _ := f.GetBool(FlagIgnoreMounted)
			flagNoChown, _ := f.GetBool(FlagNoChown)
			opts := migration.Options{
				DeleteExtraneousFiles: destDeleteExtraneousFiles,
				IgnoreMounted:         ignoreMounted,
				NoChown:               flagNoChown,
			}

			strategies, _ := f.GetStringSlice(FlagStrategies)
			rsyncImage, _ := f.GetString(FlagRsyncImage)
			sshdImage, _ := f.GetString(FlagSshdImage)
			m := migration.Migration{
				Source:     &s,
				Dest:       &d,
				Options:    &opts,
				Strategies: strategies,
				RsyncImage: rsyncImage,
				SshdImage:  sshdImage,
			}

			if opts.DeleteExtraneousFiles {
				log.Info("Extraneous files will be deleted from the destination")
			}

			return engine.New().Run(&m)
		},
	}

	f := cmd.Flags()
	f.StringP(FlagSourceKubeconfig, "k", "", "path of the kubeconfig file of the source PVC")
	f.StringP(FlagSourceContext, "c", "", "context in the kubeconfig file of the source PVC")
	f.StringP(FlagSourceNamespace, "n", "", "namespace of the source PVC")
	f.StringP(FlagDestKubeconfig, "K", "", "path of the kubeconfig file of the destination PVC")
	f.StringP(FlagDestContext, "C", "", "context in the kubeconfig file of the destination PVC")
	f.StringP(FlagDestNamespace, "N", "", "namespace of the destination PVC")
	f.BoolP(FlagDestDeleteExtraneousFiles, "d", false, "delete extraneous files on the destination by using rsync's '--delete' flag")
	f.BoolP(FlagIgnoreMounted, "i", false, "do not fail if the source or destination PVC is mounted")
	f.BoolP(FlagNoChown, "o", false, "omit chown on rsync")
	f.StringSliceP(FlagStrategies, "s", strategy.DefaultStrategies, "the comma-separated list of strategies to be used in the given order")
	f.StringP(FlagRsyncImage, "r", migration.DefaultRsyncImage, "image to use for running rsync")
	f.StringP(FlagSshdImage, "S", migration.DefaultSshdImage, "image to use for running sshd server")
	return &cmd
}
