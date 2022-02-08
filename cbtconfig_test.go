/*
Copyright 2019 Google LLC

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
<<<<<<< HEAD
package main
=======
package cbtconfig
>>>>>>> 79a35db3a (bigtable: Ignore empty lines in cbtrc)
=======
package main
>>>>>>> 017c43be2 (chore: packaging tool up into module)

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
<<<<<<< HEAD
<<<<<<< HEAD
	"time"
=======
>>>>>>> 79a35db3a (bigtable: Ignore empty lines in cbtrc)
=======
	"time"
>>>>>>> 5da19e8ea (feat(bigtable/cmd/cbt): Add a timeout option (#4276))
)

func TestReadConfig(t *testing.T) {
	project := "test-project"
	instance := "test-instance"
	credentials := "test-credentials"
	adminEndpoint := "test-admin-endpoint"
	dataEndpoint := "test-data-endpoint"
	certificateFile := "test-certificate-file"
	userAgent := "test-user-agent"
	authToken := "test-auth-token="
<<<<<<< HEAD
<<<<<<< HEAD
	timeout := time.Duration(42e9)
=======
>>>>>>> 79a35db3a (bigtable: Ignore empty lines in cbtrc)
=======
	timeout := time.Duration(42e9)
>>>>>>> 5da19e8ea (feat(bigtable/cmd/cbt): Add a timeout option (#4276))
	// Read configuration from string containing spaces, tabs and empty lines.
	validConfig := fmt.Sprintf(`
        project=%s
        instance=%s
        creds=%s
<<<<<<< HEAD
<<<<<<< HEAD
        timeout=42s
=======
>>>>>>> 79a35db3a (bigtable: Ignore empty lines in cbtrc)
=======
        timeout=42s
>>>>>>> 5da19e8ea (feat(bigtable/cmd/cbt): Add a timeout option (#4276))

        admin-endpoint =%s
        data-endpoint= %s
        cert-file=%s
        	user-agent   =  %s
           auth-token=%s  `,
		project, instance, credentials, adminEndpoint, dataEndpoint, certificateFile, userAgent, authToken)
	c, err := readConfig(bufio.NewScanner(strings.NewReader(validConfig)), "testfile")
	if err != nil {
		t.Fatalf("got unexpected error while reading config: %v", err)
	}
	if g, w := c.Project, project; g != w {
		t.Errorf("Project mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.Instance, instance; g != w {
		t.Errorf("Instance mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.Creds, credentials; g != w {
		t.Errorf("Credentials mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.AdminEndpoint, adminEndpoint; g != w {
		t.Errorf("AdminEndpoint mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.DataEndpoint, dataEndpoint; g != w {
		t.Errorf("DataEndpoint mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.CertFile, certificateFile; g != w {
		t.Errorf("CertFile mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.UserAgent, userAgent; g != w {
		t.Errorf("UserAgent mismatch\nGot: %s\nWant: %s", g, w)
	}
	if g, w := c.AuthToken, authToken; g != w {
		t.Errorf("AuthToken mismatch\nGot: %s\nWant: %s", g, w)
	}
<<<<<<< HEAD
<<<<<<< HEAD
	if g, w := c.Timeout, timeout; g != w {
		t.Errorf("AuthToken mismatch\nGot: %s\nWant: %s", g, w)
	}
=======
>>>>>>> 79a35db3a (bigtable: Ignore empty lines in cbtrc)
=======
	if g, w := c.Timeout, timeout; g != w {
		t.Errorf("AuthToken mismatch\nGot: %s\nWant: %s", g, w)
	}
>>>>>>> 5da19e8ea (feat(bigtable/cmd/cbt): Add a timeout option (#4276))

	// Try to read an invalid config file and verify that it fails.
	unknownKey := fmt.Sprintf("%s\nunknown-key=some-value", validConfig)
	_, err = readConfig(bufio.NewScanner(strings.NewReader(unknownKey)), "unknown-key-testfile")
	if err == nil {
		t.Fatalf("missing expected error in unknown-key config file")
	}
	badLine := fmt.Sprintf("%s\nproject test-project", validConfig)
	_, err = readConfig(bufio.NewScanner(strings.NewReader(badLine)), "bad-line-testfile")
	if err == nil {
		t.Fatalf("missing expected error in bad-line config file")
	}
}
