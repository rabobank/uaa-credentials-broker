package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	cfclient "github.com/cloudfoundry/go-cfclient/v3/client"
	cfconfig "github.com/cloudfoundry/go-cfclient/v3/config"

	"code.cloudfoundry.org/lager"
	"github.com/kelseyhightower/envconfig"
	"github.com/pivotal-cf/brokerapi"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// noTimestampWriterSink removes the default lager timestamp while keeping the
// rest of the JSON payload intact.
type noTimestampWriterSink struct {
	writer      io.Writer
	minLogLevel lager.LogLevel
}

func (sink noTimestampWriterSink) Log(log lager.LogFormat) {
	if log.LogLevel < sink.minLogLevel {
		return
	}

	log.Timestamp = ""
	_, _ = sink.writer.Write(append(log.ToJSON(), '\n'))
}

type Config struct {
	UAAAddress           string `envconfig:"uaa_address" required:"true"`
	UAAClientID          string `envconfig:"uaa_client_id" required:"true"`
	UAAClientSecret      string `envconfig:"uaa_client_secret" required:"true"`
	UAAZone              string `envconfig:"uaa_zone" default:"uaa"`
	UAAOrigin            string `envconfig:"uaa_origin" default:"uaa"`
	CFAddress            string `envconfig:"cf_address" required:"true"`
	BrokerUsername       string `envconfig:"broker_username" required:"true"`
	BrokerPassword       string `envconfig:"broker_password" required:"true"`
	PasswordLength       int    `envconfig:"password_length" default:"32"`
	EmailAddress         string `envconfig:"email_address" required:"true"`
	AccessTokenValidity  int    `envconfig:"access_token_validity" default:"600"`
	RefreshTokenValidity int    `envconfig:"refresh_token_validity" default:"86400"`
	Port                 string `envconfig:"port" default:"3000"`
}

func NewClient(config Config) *http.Client {
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, http.DefaultClient)
	cfg := &clientcredentials.Config{
		ClientID:     config.UAAClientID,
		ClientSecret: config.UAAClientSecret,
		TokenURL:     fmt.Sprintf("%s/oauth/token", config.UAAAddress),
	}
	return cfg.Client(ctx)
}

func main() {
	logger := lager.NewLogger("uaa-credentials-broker")
	logger.RegisterSink(noTimestampWriterSink{
		writer:      os.Stderr,
		minLogLevel: lager.INFO,
	})

	config := Config{}
	err := envconfig.Process("", &config)
	if err != nil {
		log.Fatalf("%s", err)
	}

	client := NewClient(config)

	cfConfig, _ := cfconfig.New(config.CFAddress, cfconfig.ClientCredentials(config.UAAClientID, config.UAAClientSecret))
	cfClient, err := cfclient.New(cfConfig)
	if err != nil {
		log.Fatalf("%s", err)
	}
	paasClient := &CFClient{
		Client: cfClient,
	}

	broker := DeployerAccountBroker{
		logger: logger,
		uaaClient: &UAAClient{
			logger:   logger,
			endpoint: config.UAAAddress,
			zone:     config.UAAZone,
			client:   client,
		},
		cfClient:         paasClient,
		generatePassword: GenerateSecurePassword,
		config:           config,
	}
	credentials := brokerapi.BrokerCredentials{
		Username: config.BrokerUsername,
		Password: config.BrokerPassword,
	}

	brokerAPI := brokerapi.New(&broker, logger, credentials)
	http.Handle("/", brokerAPI)
	http.ListenAndServe(fmt.Sprintf(":%s", config.Port), nil)
}
