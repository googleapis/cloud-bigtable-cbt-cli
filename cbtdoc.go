// Copyright 2016 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// DO NOT EDIT. THIS IS AUTOMATICALLY GENERATED.
// Run "go generate" to regenerate.
//go:generate go run cbt.go gcpolicy.go cbtconfig.go valueformatting.go -o cbtdoc.go doc

/*
The `cbt` CLI is a command-line interface that lets you interact with Cloud Bigtable.
See the [cbt CLI overview](https://cloud.google.com/bigtable/docs/cbt-overview) to learn how to install the `cbt` CLI.
Before you use the `cbt` CLI, you should be familiar with the [Bigtable overview](https://cloud.google.com/bigtable/docs/overview).

The examples on this page use [sample data](https://cloud.google.com/bigtable/docs/using-filters#data) similar to data
that you might store in Bigtable.

Usage:

	cbt [-<option> <option-argument>] <command> <required-argument> [optional-argument]

The commands are:

	addtocell                 Add a value to an aggregate cell (write)
	count                     Count rows in a table
	createappprofile          Create app profile for an instance
	createcluster             Create a cluster in the configured instance
	createfamily              Create a column family
	createinstance            Create an instance with an initial cluster
	createtable               Create a table
	deleteallrows             Delete all rows
	deleteappprofile          Delete app profile for an instance
	deletecluster             Delete a cluster from the configured instance
	deletecolumn              Delete all cells in a column
	deletefamily              Delete a column family
	deleteinstance            Delete an instance
	deleterow                 Delete a row
	deletetable               Delete a table
	doc                       Print godoc-suitable documentation for cbt
	getappprofile             Read app profile for an instance
	help                      Print help text
	import                    Batch write many rows based on the input file
	listappprofile            Lists app profile for an instance
	listclusters              List clusters in an instance
	listinstances             List instances in a project
	lookup                    Read from a single row
	ls                        List tables and column families
	mddoc                     Print documentation for cbt in Markdown format
	notices                   Display licence information for any third-party dependencies
	read                      Read rows
	set                       Set value of a cell (write)
	setgcpolicy               Set the garbage-collection policy (age, versions) for a column family
	updateappprofile          Update app profile for an instance
	updatecluster             Update a cluster in the configured instance
	version                   Print the current cbt version
	waitforreplication        Block until all the completed writes have been replicated to all the clusters

The options are:

	-project string
	    project ID. If unset uses gcloud configured project
	-instance string
	    Cloud Bigtable instance
	-creds string
	    Path to the credentials file. If set, uses the application credentials in this file
	-timeout string
	    Timeout (e.g. 10s, 100ms, 5m )

Example:  cbt -instance=my-instance ls

Use "cbt help \<command>" for more information about a command.

Preview features are not currently available to most Cloud Bigtable customers. Alpha
features might be changed in backward-incompatible ways and are not recommended
for production use. They are not subject to any SLA or deprecation policy.

Syntax rules for the Bash shell apply to the `cbt` CLI. This means, for example,
that you must put quotes around values that contain spaces or operators. It also means that
if a value is arbitrary bytes, you need to prefix it with a dollar sign and use single quotes.

Example:

cbt -project my-project -instance my-instance lookup my-table $'\224\257\312W\365:\205d\333\2471\315\'

For convenience, you can add values for the -project, -instance, -creds, -admin-endpoint and -data-endpoint
options to your ~/.cbtrc file in the following format:

	project = my-project-123
	instance = my-instance
	creds = path-to-account-key.json
	admin-endpoint = hostname:port
	data-endpoint = hostname:port
	auth-token = AJAvW039NO1nDcijk_J6_rFXG_...
	timeout = 30s

All values are optional and can be overridden at the command prompt.

Add a value to an aggregate cell (write)

Usage:

	cbt addtocell <table-id> <row-key> [app-profile=<app-profile-id>] <family>:<column>=<val>[@<timestamp>] ...
	  app-profile=<app profile id>          The app profile ID to use for the request
	  <family>:<column>=<val>[@<timestamp>] may be repeated to set multiple cells.

	    If <val> can be parsed as an integer it will be used as one, otherwise the call will fail.
	    timestamp is an optional integer.
	    If the timestamp cannot be parsed, '@<timestamp>' will be interpreted as part of the value.
	    For most uses, a timestamp is the number of microseconds since 1970-01-01 00:00:00 UTC.

	    Examples:
	      cbt addtocell table1 user1 sum_cf:col1=1@12345

# Count rows in a table

Usage:

	cbt count <table-id> [prefix=<row-key-prefix>]

# Create app profile for an instance

Usage:

	cbt createappprofile <instance-id> <app-profile-id> <description> (route-any | [ route-to=<cluster-id> : transactional-writes]) [-force]
	  force:  Optional flag to override any warnings causing the command to fail

	    Examples:
	      cbt createappprofile my-instance multi-cluster-app-profile-1 "Routes to nearest available cluster" route-any
	      cbt createappprofile my-instance single-cluster-app-profile-1 "Europe routing" route-to=my-instance-cluster-2

# Create a cluster in the configured instance

Usage:

	cbt createcluster <cluster-id> <zone> <num-nodes> <storage-type>
	  cluster-id       Permanent, unique ID for the cluster in the instance
	  zone             The zone in which to create the cluster
	  num-nodes        The number of nodes to create
	  storage-type     SSD or HDD

	    Example: cbt createcluster my-instance-c2 europe-west1-b 3 SSD

# Create a column family

Usage:

	cbt createfamily <table-id> <family>

	    Example: cbt createfamily mobile-time-series stats_summary

# Create an instance with an initial cluster

Usage:

	cbt createinstance <instance-id> <display-name> <cluster-id> <zone> <num-nodes> <storage-type>
	  instance-id      Permanent, unique ID for the instance
	  display-name     Description of the instance
	  cluster-id       Permanent, unique ID for the cluster in the instance
	  zone             The zone in which to create the cluster
	  num-nodes        The number of nodes to create
	  storage-type     SSD or HDD

	    Example: cbt createinstance my-instance "My instance" my-instance-c1 us-central1-b 3 SSD

# Create a table

Usage:

	cbt createtable <table-id> [families=<family>:<gcpolicy-expression>:<type-expression>,...]
	   [splits=<split-row-key-1>,<split-row-key-2>,...]
	  families     Column families and their associated garbage collection (gc) policies and types.
	               Put gc policies in quotes when they include shell operators && and ||. For gcpolicy,
	               see "setgcpolicy".
	               Currently only the type "intsum" is supported.
	  splits       Row key(s) where the table should initially be split

	    Example: cbt createtable mobile-time-series "families=stats_summary:maxage=10d||maxversions=1,stats_detail:maxage=10d||maxversions=1" splits=tablet,phone

# Delete all rows

Usage:

	cbt deleteallrows <table-id>

	    Example: cbt deleteallrows  mobile-time-series

# Delete app profile for an instance

Usage:

	cbt deleteappprofile <instance-id> <profile-id>

	    Example: cbt deleteappprofile my-instance single-cluster

# Delete a cluster from the configured instance

Usage:

	cbt deletecluster <cluster-id>

	    Example: cbt deletecluster my-instance-c2

# Delete all cells in a column

Usage:

	cbt deletecolumn <table-id> <row-key> <family> <column> [app-profile=<app-profile-id>]
	  app-profile=<app-profile-id>        The app profile ID to use for the request

	    Example: cbt deletecolumn mobile-time-series phone#4c410523#20190501 stats_summary os_name

# Delete a column family

Usage:

	cbt deletefamily <table-id> <family>

	    Example: cbt deletefamily mobile-time-series stats_summary

# Delete an instance

Usage:

	cbt deleteinstance <instance-id>

	    Example: cbt deleteinstance my-instance

# Delete a row

Usage:

	cbt deleterow <table-id> <row-key> [app-profile=<app-profile-id>]
	  app-profile=<app-profile-id>        The app profile ID to use for the request

	    Example: cbt deleterow mobile-time-series phone#4c410523#20190501

# Delete a table

Usage:

	cbt deletetable <table-id>

	    Example: cbt deletetable mobile-time-series

# Print godoc-suitable documentation for cbt

Usage:

	cbt doc

# Read app profile for an instance

Usage:

	cbt getappprofile <instance-id> <profile-id>

# Print help text

Usage:

	cbt help <command>

	    Example: cbt help createtable

# Batch write many rows based on the input file

Usage:

	cbt import <table-id> <input-file> [app-profile=<app-profile-id>] [column-family=<family-name>] [batch-size=<500>] [workers=<1>] [timestamp=<now|value-encoded>]
	  app-profile=<app-profile-id>          The app profile ID to use for the request
	  column-family=<family-name>           The column family label to use
	  batch-size=<500>                      The max number of rows per batch write request
	  workers=<1>                           The number of worker threads
	  timestamp=<now|value-encoded>	     	Whether to use current time for all cells or interpret the timestamp from cell value. Defaults to 'now'.

	  Import data from a CSV file into an existing Cloud Bigtable table that already has the column families your data requires.

	  The CSV file can support two rows of headers:
	      - (Optional) column families
	      - Column qualifiers
	  Because the first column is reserved for row keys, leave it empty in the header rows.
	  In the column family header, provide each column family once; it applies to the column it is in and every column to the right until another column family is found.
	  Each row after the header rows should contain a row key in the first column, followed by the data cells for the row.
	  See the example below. If you don't provide a column family header row, the column header is your first row and your import command must include the `column-family` flag to specify an existing column family.

	  The timestamp for each cell will default to current time (timestamp=now), to explicitly set the timestamp for cells, set timestamp=value-encoded use <val>[@<timestamp>] as the value for the cell.
	  If no timestamp is delimited for a cell, current time will be used. If the timestamp cannot be parsed, '@<timestamp>' will be interpreted as part of the value.
	  For most uses, a timestamp is the number of microseconds since 1970-01-01 00:00:00 UTC.

	    ,column-family-1,,column-family-2,      // Optional column family row (1st cell empty)
	    ,column-1,column-2,column-3,column-4    // Column qualifiers row (1st cell empty)
	    a,TRUE,,,FALSE                          // Rowkey 'a' followed by data
	    b,,,TRUE,FALSE                          // Rowkey 'b' followed by data
	    c,,TRUE,,TRUE                           // Rowkey 'c' followed by data
	    d,TRUE@1577862000000000,,,FALSE		 	// Rowkey 'd' followed by data

	  Examples:
	    cbt import csv-import-table data.csv
	    cbt import csv-import-table data-no-families.csv app-profile=batch-write-profile column-family=my-family workers=5

# Lists app profile for an instance

Usage:

	cbt listappprofile <instance-id>

# List clusters in an instance

Usage:

	cbt listclusters

# List instances in a project

Usage:

	cbt listinstances

# Read from a single row

Usage:

	cbt lookup <table-id> <row-key> [columns=<family>:<qualifier>,...] [cells-per-column=<n>] [app-profile=<app profile id>]
	  row-key                             String or raw bytes. Raw bytes must be enclosed in single quotes and have a dollar-sign prefix
	  columns=<family>:<qualifier>,...    Read only these columns, comma-separated
	  cells-per-column=<n>                Read only this number of cells per column
	  app-profile=<app-profile-id>        The app profile ID to use for the request
	  format-file=<path-to-format-file>   The path to a format-configuration file to use for the request
	  keys-only=<true|false>              Whether to print only row keys
	  include-stats=full                  Include a summary of request stats at the end of the request

	 Example: cbt lookup mobile-time-series phone#4c410523#20190501 columns=stats_summary:os_build,os_name cells-per-column=1
	 Example: cbt lookup mobile-time-series $'\x41\x42'

# List tables and column families

Usage:

	cbt ls                List tables
	cbt ls <table-id>     List a table's column families and garbage collection policies

	    Example: cbt ls mobile-time-series

# Print documentation for cbt in Markdown format

Usage:

	cbt mddoc

# Display licence information for any third-party dependencies

Usage:

	cbt notices

# Read rows

Usage:

	cbt read <table-id> [start=<row-key>] [end=<row-key>] [prefix=<row-key-prefix>] [regex=<regex>] [columns=<family>:<qualifier>,...] [count=<n>] [cells-per-column=<n>] [app-profile=<app-profile-id>]
	  start=<row-key>                     Start reading at this row
	  end=<row-key>                       Stop reading before this row
	  prefix=<row-key-prefix>             Read rows with this prefix
	  regex=<regex>                       Read rows with keys matching this regex
	  reversed=<true|false>               Read rows in reverse order
	  columns=<family>:<qualifier>,...    Read only these columns, comma-separated
	  count=<n>                           Read only this many rows
	  cells-per-column=<n>                Read only this many cells per column
	  app-profile=<app-profile-id>        The app profile ID to use for the request
	  format-file=<path-to-format-file>   The path to a format-configuration file to use for the request
	  keys-only=<true|false>              Whether to print only row keys
	  include-stats=full                  Include a summary of request stats at the end of the request

	    Examples: (see 'set' examples to create data to read)
	      cbt read mobile-time-series prefix=phone columns=stats_summary:os_build,os_name count=10
	      cbt read mobile-time-series start=phone#4c410523#20190501 end=phone#4c410523#20190601
	      cbt read mobile-time-series regex="phone.*" cells-per-column=1
	      cbt read mobile-time-series start=phone#4c410523#20190501 end=phone#4c410523#20190601 reversed=true count=10

	   Note: Using a regex without also specifying start, end, prefix, or count results in a full
	   table scan, which can be slow.

Set value of a cell (write)

Usage:

	cbt set <table-id> <row-key> [app-profile=<app-profile-id>] <family>:<column>=<val>[@<timestamp>] ...
	  app-profile=<app profile id>          The app profile ID to use for the request
	  <family>:<column>=<val>[@<timestamp>] may be repeated to set multiple cells.

	    timestamp is an optional integer.
	    If the timestamp cannot be parsed, '@<timestamp>' will be interpreted as part of the value.
	    For most uses, a timestamp is the number of microseconds since 1970-01-01 00:00:00 UTC.

	    Examples:
	      cbt set mobile-time-series phone#4c410523#20190501 stats_summary:connected_cell=1@12345 stats_summary:connected_cell=0@1570041766
	      cbt set mobile-time-series phone#4c410523#20190501 stats_summary:os_build=PQ2A.190405.003 stats_summary:os_name=android

# Set the garbage-collection policy (age, versions) for a column family

Usage:

	cbt setgcpolicy <table> <family> ((maxage=<d> | maxversions=<n>) [(and|or) (maxage=<d> | maxversions=<n>),...] | never)
	  maxage=<d>         Maximum timestamp age to preserve. Acceptable units: ms, s, m, h, d
	  maxversions=<n>    Maximum number of versions to preserve
	  Put garbage collection policies in quotes when they include shell operators && and ||.

	    Examples:
	      cbt setgcpolicy mobile-time-series stats_detail maxage=10d
	      cbt setgcpolicy mobile-time-series stats_summary maxage=10d or maxversions=1

# Update app profile for an instance

Usage:

	cbt updateappprofile  <instance-id> <profile-id> <description>(route-any | [ route-to=<cluster-id> : transactional-writes]) [-force]
	  force:  Optional flag to override any warnings causing the command to fail

	    Example: cbt updateappprofile my-instance multi-cluster-app-profile-1 "Use this one." route-any

# Update a cluster in the configured instance

Usage:

	cbt updatecluster <cluster-id> [num-nodes=<num-nodes>]
	  cluster-id    Permanent, unique ID for the cluster in the instance
	  num-nodes     The new number of nodes

	    Example: cbt updatecluster my-instance-c1 num-nodes=5

# Print the current cbt version

Usage:

	cbt version

# Block until all the completed writes have been replicated to all the clusters

Usage:

	cbt waitforreplication <table-id>
*/
package main
