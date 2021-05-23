package app

import (
	"errors"
	"fmt"
	"github.com/mitchellh/go-homedir"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	FlagSourceKubeconfig          = "source-kubeconfig"
	FlagSourceContext             = "source-context"
	FlagSourceNamespace           = "source-namespace"
	FlagDestKubeconfig            = "dest-kubeconfig"
	FlagDestContext               = "dest-context"
	FlagDestNamespace             = "dest-namespace"
	FlagDestDeleteExtraneousFiles = "dest-delete-extraneous-files"
	FlagIgnoreMounted             = "ignore-mounted"
	FlagNoChown                   = "no-chown"
	FlagStrategies                = "strategies"
	FlagRsyncImage                = "rsync-image"
	FlagSshdImage                 = "sshd-image"
)

const (
	appName = "pv-migrate"
)

var cfgFile string

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := homedir.Dir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.SetConfigName(fmt.Sprintf(".%s", appName))
	}

	viper.AutomaticEnv()
	err := viper.ReadInConfig()

	if err == nil {
		log.WithField("config", viper.ConfigFileUsed()).Info("Using config file")
		return
	}

	var notFoundError = viper.ConfigFileNotFoundError{}
	if !errors.As(err, &notFoundError) {
		log.WithError(err).Warn("Failed to read config from file")
	}
}

func New(version string, commit string) *cobra.Command {
	return buildRootCmd(version, commit)
}
