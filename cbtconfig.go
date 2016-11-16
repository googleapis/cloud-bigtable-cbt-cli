/*
<<<<<<< HEAD
Copyright 2015 Google LLC
=======
Copyright 2015 Google Inc. All Rights Reserved.
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

<<<<<<< HEAD
package main
=======
// Package cbtconfig encapsulates common code for reading configuration from .cbtrc and gcloud.
package cbtconfig
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)

import (
	"bufio"
	"bytes"
<<<<<<< HEAD
	"crypto/tls"
	"crypto/x509"
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
<<<<<<< HEAD
=======
	"os/exec"
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
<<<<<<< HEAD
	"golang.org/x/sys/execabs"
	"google.golang.org/grpc/credentials"
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
)

// Config represents a configuration.
type Config struct {
<<<<<<< HEAD
	Project, Instance string                           // required
	Creds             string                           // optional
	AdminEndpoint     string                           // optional
	DataEndpoint      string                           // optional
	CertFile          string                           // optional
	UserAgent         string                           // optional
	AuthToken         string                           // optional
	Timeout           time.Duration                    // optional
	TokenSource       oauth2.TokenSource               // derived
	TLSCreds          credentials.TransportCredentials // derived
}

// RequiredFlags describes the flag requirements for a cbt command.
type RequiredFlags uint

const (
	// NoneRequired specifies that not flags are required.
	NoneRequired RequiredFlags = 0
	// ProjectRequired specifies that the -project flag is required.
	ProjectRequired RequiredFlags = 1 << iota
	// InstanceRequired specifies that the -instance flag is required.
	InstanceRequired
	// ProjectAndInstanceRequired specifies that both -project and -instance is required.
	ProjectAndInstanceRequired = ProjectRequired | InstanceRequired
)
=======
	Project, Instance string             // required
	Creds             string             // optional
	AdminEndpoint     string             // optional
	DataEndpoint      string             // optional
	TokenSource       oauth2.TokenSource // derived
}

type RequiredFlags uint

const NoneRequired RequiredFlags = 0
const (
	ProjectRequired RequiredFlags = 1 << iota
	InstanceRequired
)
const ProjectAndInstanceRequired RequiredFlags = ProjectRequired | InstanceRequired
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)

// RegisterFlags registers a set of standard flags for this config.
// It should be called before flag.Parse.
func (c *Config) RegisterFlags() {
<<<<<<< HEAD
	flag.StringVar(&c.Project, "project", c.Project, "project ID. If unset uses gcloud configured project")
	flag.StringVar(&c.Instance, "instance", c.Instance, "Cloud Bigtable instance")
	flag.StringVar(&c.Creds, "creds", c.Creds, "Path to the credentials file. If set, uses the application credentials in this file")
	flag.StringVar(&c.AdminEndpoint, "admin-endpoint", c.AdminEndpoint, "Override the admin api endpoint")
	flag.StringVar(&c.DataEndpoint, "data-endpoint", c.DataEndpoint, "Override the data api endpoint")
	flag.StringVar(&c.CertFile, "cert-file", c.CertFile, "Override the TLS certificates file")
	flag.StringVar(&c.UserAgent, "user-agent", c.UserAgent, "Override the user agent string")
	flag.StringVar(&c.AuthToken, "auth-token", c.AuthToken, "if set, use IAM Auth Token for requests")
	flag.DurationVar(&c.Timeout, "timeout", c.Timeout,
		"Timeout (e.g. 10s, 100ms, 5m )")
=======
	flag.StringVar(&c.Project, "project", c.Project, "project ID, if unset uses gcloud configured project")
	flag.StringVar(&c.Instance, "instance", c.Instance, "Cloud Bigtable instance")
	flag.StringVar(&c.Creds, "creds", c.Creds, "if set, use application credentials in this file")
	flag.StringVar(&c.AdminEndpoint, "admin-endpoint", c.AdminEndpoint, "Override the admin api endpoint")
	flag.StringVar(&c.DataEndpoint, "data-endpoint", c.DataEndpoint, "Override the data api endpoint")
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
}

// CheckFlags checks that the required config values are set.
func (c *Config) CheckFlags(required RequiredFlags) error {
	var missing []string
<<<<<<< HEAD
	if c.CertFile != "" {
		b, err := ioutil.ReadFile(c.CertFile)
		if err != nil {
			return fmt.Errorf("failed to load certificates from %s: %v", c.CertFile, err)
		}

		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(b) {
			return fmt.Errorf("failed to append certificates from %s", c.CertFile)
		}

		c.TLSCreds = credentials.NewTLS(&tls.Config{RootCAs: cp})
	}
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
	if required != NoneRequired {
		c.SetFromGcloud()
	}
	if required&ProjectRequired != 0 && c.Project == "" {
		missing = append(missing, "-project")
	}
	if required&InstanceRequired != 0 && c.Instance == "" {
		missing = append(missing, "-instance")
	}
	if len(missing) > 0 {
<<<<<<< HEAD
		return fmt.Errorf("missing %s", strings.Join(missing, " and "))
=======
		return fmt.Errorf("Missing %s", strings.Join(missing, " and "))
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
	}
	return nil
}

// Filename returns the filename consulted for standard configuration.
func Filename() string {
	// TODO(dsymonds): Might need tweaking for Windows.
	return filepath.Join(os.Getenv("HOME"), ".cbtrc")
}

// Load loads a .cbtrc file.
// If the file is not present, an empty config is returned.
func Load() (*Config, error) {
	filename := Filename()
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		// silent fail if the file isn't there
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
<<<<<<< HEAD
		return nil, fmt.Errorf("reading %s: %v", filename, err)
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	return readConfig(s, filename)
}

func readConfig(s *bufio.Scanner, filename string) (*Config, error) {
	c := new(Config)
	for s.Scan() {
		line := s.Text()
		// Ignore empty lines.
		if strings.TrimSpace(line) == "" {
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 {
			return nil, fmt.Errorf("bad line in %s: %q", filename, line)
=======
		return nil, fmt.Errorf("Reading %s: %v", filename, err)
	}
	c := new(Config)
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		line := s.Text()
		i := strings.Index(line, "=")
		if i < 0 {
			return nil, fmt.Errorf("Bad line in %s: %q", filename, line)
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
		}
		key, val := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		switch key {
		default:
<<<<<<< HEAD
			return nil, fmt.Errorf("unknown key in %s: %q", filename, key)
=======
			return nil, fmt.Errorf("Unknown key in %s: %q", filename, key)
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
		case "project":
			c.Project = val
		case "instance":
			c.Instance = val
		case "creds":
			c.Creds = val
		case "admin-endpoint":
			c.AdminEndpoint = val
		case "data-endpoint":
			c.DataEndpoint = val
<<<<<<< HEAD
		case "cert-file":
			c.CertFile = val
		case "user-agent":
			c.UserAgent = val
		case "auth-token":
			c.AuthToken = val
		case "timeout":
			timeout, err := time.ParseDuration(val)
			if err != nil {
				return nil, err
			}
			c.Timeout = timeout
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
		}

	}
	return c, s.Err()
}

<<<<<<< HEAD
// GcloudCredential holds gcloud credential information.
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
type GcloudCredential struct {
	AccessToken string    `json:"access_token"`
	Expiry      time.Time `json:"token_expiry"`
}

<<<<<<< HEAD
// Token creates an oauth2 token using gcloud credentials.
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
func (cred *GcloudCredential) Token() *oauth2.Token {
	return &oauth2.Token{AccessToken: cred.AccessToken, TokenType: "Bearer", Expiry: cred.Expiry}
}

<<<<<<< HEAD
// GcloudConfig holds gcloud configuration values.
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
type GcloudConfig struct {
	Configuration struct {
		Properties struct {
			Core struct {
				Project string `json:"project"`
			} `json:"core"`
		} `json:"properties"`
	} `json:"configuration"`
	Credential GcloudCredential `json:"credential"`
}

<<<<<<< HEAD
// GcloudCmdTokenSource holds the comamnd arguments. It is only intended to be set by the program.
// TODO(deklerk): Can this be unexported?
=======
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
type GcloudCmdTokenSource struct {
	Command string
	Args    []string
}

// Token implements the oauth2.TokenSource interface
func (g *GcloudCmdTokenSource) Token() (*oauth2.Token, error) {
	gcloudConfig, err := LoadGcloudConfig(g.Command, g.Args)
	if err != nil {
		return nil, err
	}
	return gcloudConfig.Credential.Token(), nil
}

// LoadGcloudConfig retrieves the gcloud configuration values we need use via the
// 'config-helper' command
func LoadGcloudConfig(gcloudCmd string, gcloudCmdArgs []string) (*GcloudConfig, error) {
<<<<<<< HEAD
	out, err := execabs.Command(gcloudCmd, gcloudCmdArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("could not retrieve gcloud configuration")
=======
	out, err := exec.Command(gcloudCmd, gcloudCmdArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("Could not retrieve gcloud configuration")
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
	}

	var gcloudConfig GcloudConfig
	if err := json.Unmarshal(out, &gcloudConfig); err != nil {
<<<<<<< HEAD
		return nil, fmt.Errorf("could not parse gcloud configuration")
	}

=======
		return nil, fmt.Errorf("Could not parse gcloud configuration")
	}

	log.Printf("Retrieved gcloud configuration, active project is \"%s\"",
		gcloudConfig.Configuration.Properties.Core.Project)
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
	return &gcloudConfig, nil
}

// SetFromGcloud retrieves and sets any missing config values from the gcloud
// configuration if possible possible
func (c *Config) SetFromGcloud() error {

	if c.Creds == "" {
		c.Creds = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		if c.Creds == "" {
			log.Printf("-creds flag unset, will use gcloud credential")
		}
	} else {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", c.Creds)
	}

	if c.Project == "" {
		log.Printf("-project flag unset, will use gcloud active project")
	}

	if c.Creds != "" && c.Project != "" {
		return nil
	}

	gcloudCmd := "gcloud"
	if runtime.GOOS == "windows" {
		gcloudCmd = gcloudCmd + ".cmd"
	}

	gcloudCmdArgs := []string{"config", "config-helper",
		"--format=json(configuration.properties.core.project,credential)"}

	gcloudConfig, err := LoadGcloudConfig(gcloudCmd, gcloudCmdArgs)
	if err != nil {
		return err
	}

<<<<<<< HEAD
	if c.Project == "" && gcloudConfig.Configuration.Properties.Core.Project != "" {
		log.Printf("gcloud active project is \"%s\"",
			gcloudConfig.Configuration.Properties.Core.Project)
=======
	if c.Project == "" {
>>>>>>> b3333af8e (bigtable: use gcloud config-helper for project and creds in cbt)
		c.Project = gcloudConfig.Configuration.Properties.Core.Project
	}

	if c.Creds == "" {
		c.TokenSource = oauth2.ReuseTokenSource(
			gcloudConfig.Credential.Token(),
			&GcloudCmdTokenSource{Command: gcloudCmd, Args: gcloudCmdArgs})
	}

	return nil
}
