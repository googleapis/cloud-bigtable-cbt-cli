/*
Copyright 2022 Google LLC

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

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"

	"cloud.google.com/go/bigtable"
)

func TestParseValueFormatSettings(t *testing.T) {
	want := valueFormatSettings{
		DefaultEncoding:           "HEX",
		ProtocolBufferDefinitions: []string{"MyProto.proto", "MyOtherProto.proto"},
		ProtocolBufferPaths:       []string{"mycode/stuff", "/home/user/dev/othercode/"},
		Columns: map[string]valueFormatColumn{
			"col3": {
				Encoding: "P",
				Type:     "person",
			},
			"col4": {
				Encoding: "P",
				Type:     "hobby",
			},
		},
		Families: map[string]valueFormatFamily{
			"family1": {
				DefaultEncoding: "BigEndian",
				DefaultType:     "INT64",
				Columns: map[string]valueFormatColumn{
					"address": {
						Encoding: "PROTO",
						Type:     "tutorial.Person",
					},
				},
			},

			"family2": {
				Columns: map[string]valueFormatColumn{
					"col1": {
						Encoding: "B",
						Type:     "INT32",
					},
					"col2": {
						Encoding: "L",
						Type:     "INT16",
					},
					"address": {
						Encoding: "PROTO",
						Type:     "tutorial.Person",
					},
				},
			},
			"family3": {
				Columns: map[string]valueFormatColumn{
					"proto_col": {
						Encoding: "PROTO",
						Type:     "MyProtoMessageType",
					},
				},
			},
		},
	}

	formatting := newValueFormatting()

	err := formatting.parse(filepath.Join("testdata", t.Name()+".yml"))
	if err != nil {
		t.Errorf("Parse error: %s", err)
	}
	if !cmp.Equal(formatting.settings, want) {
		t.Error("Formatting error: formatting settings don't match return value")
	}
}

func TestSetupPBMessages(t *testing.T) {

	formatting := newValueFormatting()

	formatting.settings.ProtocolBufferPaths = append(
		formatting.settings.ProtocolBufferPaths,
		"testdata")
	formatting.settings.ProtocolBufferPaths = append(
		formatting.settings.ProtocolBufferPaths,
		filepath.Join("testdata", "protoincludes"))
	formatting.settings.ProtocolBufferDefinitions = append(
		formatting.settings.ProtocolBufferDefinitions,
		"addressbook.proto")
	formatting.settings.ProtocolBufferDefinitions = append(
		formatting.settings.ProtocolBufferDefinitions,
		"club.proto")
	err := formatting.setupPBMessages()
	if err != nil {
		t.Errorf("Proto parse error: %s", err)
		return
	}

	var keys []string
	for k := range formatting.pbMessageTypes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	want := []string{
		"addressbook",
		"equipment",
		"person",
		"tutorial.addressbook",
		"tutorial.person",
	}

	if !cmp.Equal(keys, want) {
		t.Errorf("Protobuf keys not set correctly: wanted: %s; got %s",
			want, keys)
	}

	// Make sure the message descriptors are usable.
	message := dynamic.NewMessage(formatting.pbMessageTypes["tutorial.person"])
	in, err := ioutil.ReadFile(filepath.Join("testdata", "person.bin"))
	if err != nil {
		t.Error("Not able to open testdata (person.bin)")
	}

	err = message.Unmarshal(in)
	if err != nil {
		t.Error("Protobuf message not correctly deserialized")
	}

	wantFormatted := string(`name:"Jim" id:42 email:"jim@example.com"` +
		` phones:<number:"555-1212" type:HOME>`)
	gotFormatted := fmt.Sprint(message)

	if gotFormatted != wantFormatted {
		t.Errorf("Format error: wanted %s; got %s", wantFormatted,
			gotFormatted)
	}
}

var TestBinaryFormaterTestData = []byte{
	0, 1, 2, 3, 4, 5, 6, 7, 255, 255, 255, 255, 255, 255, 255, 156}

func checkBinaryValueFormatter(
	t *testing.T, ctype string, nbytes int, expect string, order binary.ByteOrder,
) {
	s, err :=
		binaryValueFormatters[ctype](TestBinaryFormaterTestData[:nbytes], order)

	if err != nil {
		t.Errorf("Error formatting binary value: %v", err)
	}

	if s != expect {
		t.Errorf("Binary value formatted incorrectly: wanted %s; got %s",
			expect, s)
	}
}

func TestBinaryValueFormaterINT8(t *testing.T) {
	checkBinaryValueFormatter(
		t, "int8", 16, "[0 1 2 3 4 5 6 7 -1 -1 -1 -1 -1 -1 -1 -100]", binary.BigEndian)
}

func TestBinaryValueFormaterINT16(t *testing.T) {
	// Main test that tests special handling of arrays vs scalers, etc.

	checkBinaryValueFormatter(
		t, "int16", 16, "[1 515 1029 1543 -1 -1 -1 -100]", binary.BigEndian)
	checkBinaryValueFormatter(t, "int16", 0, "[]", binary.BigEndian)
	checkBinaryValueFormatter(t, "int16", 2, "1", binary.BigEndian)
	checkBinaryValueFormatter(
		t, "int16", 16, "[256 770 1284 1798 -1 -1 -1 -25345]", binary.LittleEndian)
}

func TestBinaryValueFormaterINT32(t *testing.T) {
	checkBinaryValueFormatter(
		t, "int32", 16, "[66051 67438087 -1 -100]", binary.BigEndian)
}

func TestBinaryValueFormaterINT64(t *testing.T) {
	checkBinaryValueFormatter(
		t, "int64", 16, "[283686952306183 -100]", binary.BigEndian)
}

func TestBinaryValueFormaterUINT8(t *testing.T) {
	checkBinaryValueFormatter(
		t, "uint8", 16, "[0 1 2 3 4 5 6 7 255 255 255 255 255 255 255 156]",
		binary.BigEndian)
}

func TestBinaryValueFormaterUINT16(t *testing.T) {
	checkBinaryValueFormatter(
		t, "uint16", 16, "[1 515 1029 1543 65535 65535 65535 65436]",
		binary.BigEndian)
}

func TestBinaryValueFormaterUINT32(t *testing.T) {
	checkBinaryValueFormatter(
		t, "uint32", 16, "[66051 67438087 4294967295 4294967196]", binary.BigEndian)
}

func TestBinaryValueFormaterUINT64(t *testing.T) {
	checkBinaryValueFormatter(
		t, "uint64", 16, "[283686952306183 18446744073709551516]", binary.BigEndian)
}

func TestBinaryValueFormaterFLOAT32(t *testing.T) {
	checkBinaryValueFormatter(
		t, "float32", 16, "[9.2557e-41 1.5636842e-36 NaN NaN]", binary.BigEndian)
}

func TestBinaryValueFormaterFLOAT64(t *testing.T) {
	checkBinaryValueFormatter(
		t, "float64", 16, "[1.40159977307889e-309 NaN]", binary.BigEndian)
}

func TestValueFormattingBinaryFormatter(t *testing.T) {
	formatting := newValueFormatting()
	var formatter = formatting.binaryFormatter(bigEndian, "int32")
	s, err := formatter(TestBinaryFormaterTestData)
	want := "[66051 67438087 -1 -100]"

	if err != nil {
		t.Errorf("Error when creating formatter: %v", err)
	}
	if s != want {
		t.Errorf("Binary value formatted incorrectly: wanted %s, got %s",
			want, s)
	}

	formatter = formatting.binaryFormatter(littleEndian, "int32")
	s, err = formatter(TestBinaryFormaterTestData)
	want = "[50462976 117835012 -1 -1660944385]"

	if err != nil {
		t.Errorf("Error when creating formatter: %v", err)
	}

	if s != want {
		t.Errorf("Binary value formatted incorrectly: wanted %s, got %s",
			want, s)
	}
}

func TestValueFormattingJSONFormatter(t *testing.T) {
	vf := newValueFormatting()
	f, err := vf.jsonFormatter()

	if err != nil {
		t.Errorf("Error creating formatter: %v", err)
	}

	s := []byte("{\"name\": \"Brave\", \"age\": 2, \"isFluffy\": true, \"hobbies\": { \"toys\": [ \"mousies\"]}}")
	got, err := f(s)
	want := `age:     2.00
hobbies: 
  toys: 
    [
      "mousies"
    ]

isFluffy: true
name:   "Brave"`

	if err != nil {
		t.Errorf("Error formatting JSON string: %v", err)
	}

	if !strings.Contains(got, want) {
		t.Errorf("JSON not formatted correctly; wanted:\n%v\n; got:\n%v\n",
			want, got)
	}
}

func TestValueFormattingPBFormatter(t *testing.T) {
	formatting := newValueFormatting()
	formatting.settings.ProtocolBufferDefinitions = append(
		formatting.settings.ProtocolBufferDefinitions,
		filepath.Join("testdata", "addressbook.proto"))
	err := formatting.setupPBMessages()
	if err != nil {
		t.Errorf("Error creating protobuf formatter: %v", err)
	}

	formatter, err := formatting.pbFormatter("person")
	if err != nil {
		t.Error("Could not create protobuf formatter")
	}

	in, err := ioutil.ReadFile(filepath.Join("testdata", "person.bin"))
	if err != nil {
		t.Errorf("Error reading testdata: %v", err)
	}

	got, err := formatter(in)
	want := `name:  "Jim"
id:  42
email:  "jim@example.com"
phones:  {
  number:  "555-1212"
  type:  HOME
}
`

	if err != nil {
		t.Errorf("Error creating protobuf formatter: %v", err)
	}

	if got != want {
		t.Errorf("Protobuf not formatted correctly: wanted %s; got %s",
			want, got)
	}

	in, err = ioutil.ReadFile(filepath.Join("testdata", "person_ko.bin"))
	if err != nil {
		t.Errorf("Error reading testdata: %v", err)
	}

	got, err = formatter(in)
	want = `name:  "민수"
id:  37
email:  "minsu@example.com"
phones:  {
  number:  "555-1213"
  type:  WORK
}
`

	if err != nil {
		t.Errorf("Error creating protobuf formatter: %v", err)
	}

	if got != want {
		t.Errorf("Protobuf not formatted correctly: wanted %s; got %s",
			want, got)
	}

	_, err = formatting.pbFormatter("not a thing")
	if err == nil {
		t.Error("Protobuf formatter created with bad input")
	}
}

func TestValueFormattingValidateColumns(t *testing.T) {
	formatting := newValueFormatting()

	// Typeless encoding:
	formatting.settings.Columns["c1"] = valueFormatColumn{Encoding: "HEX"}
	err := formatting.validateColumns()
	if err != nil {
		t.Errorf("Error validating columns: %v", err)
	}

	// Inherit encoding:
	formatting.settings.Columns["c1"] = valueFormatColumn{}
	formatting.settings.DefaultEncoding = "H"
	err = formatting.validateColumns()
	if err != nil {
		t.Errorf("Error validating columns: %v", err)
	}

	// Inherited encoding wants a type:
	formatting.settings.DefaultEncoding = "B"
	err = formatting.validateColumns()
	got := fmt.Sprint(err)
	want := "bad encoding and types:\nc1: no type specified for encoding: B"

	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}

	// provide a type:
	formatting.settings.Columns["c1"] = valueFormatColumn{Type: "INT"}
	err = formatting.validateColumns()
	got = fmt.Sprint(err)
	want = "bad encoding and types:\nc1: invalid type: INT for encoding: B"

	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}

	// Fix the type:
	formatting.settings.Columns["c1"] = valueFormatColumn{Type: "INT64"}
	err = formatting.validateColumns()
	if err != nil {
		t.Errorf("Error validating columns: %v", err)
	}

	// Now, do a bunch of this again in a family
	family := newValueFormatFamily()
	formatting.settings.Families["f"] = family
	formatting.settings.Families["f"].Columns["c2"] = valueFormatColumn{}
	err = formatting.validateColumns()
	got = fmt.Sprint(err)
	want = "bad encoding and types:\nf:c2: no type specified for encoding: B"

	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}
	formatting.settings.Families["f"].Columns["c2"] =
		valueFormatColumn{Type: "int64"}
	err = formatting.validateColumns()
	if err != nil {
		t.Errorf("Error validating columns: %v", err)
	}

	// Change the family encoding.  The type won't work anymore.
	family.DefaultEncoding = "p"
	formatting.settings.Families["f"] = family
	err = formatting.validateColumns()
	got = fmt.Sprint(err)
	want = "bad encoding and types:\nf:c2: invalid type: int64 for encoding: p"

	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}

	// clear the type_ to make sure we get that message:
	formatting.settings.Families["f"].Columns["c2"] = valueFormatColumn{}
	err = formatting.validateColumns()
	// we're bad here because no type was specified, so we fall
	// back to the column name, which doesn't have a
	// protocol-buffer message type.
	want = fmt.Sprint(err)
	got = "bad encoding and types:\nf:c2: invalid type: c2 for encoding: p"

	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}

	// Look! Multiple errors!
	formatting.settings.Columns["c1"] = valueFormatColumn{}
	err = formatting.validateColumns()
	got = fmt.Sprint(err)
	want = "bad encoding and types:\n" +
		"c1: no type specified for encoding: B\n" +
		"f:c2: invalid type: c2 for encoding: p"
	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}

	// Fix the protocol-buffer problem:
	formatting.pbMessageTypes["address"] = &desc.MessageDescriptor{}
	formatting.settings.Families["f"].Columns["c2"] =
		valueFormatColumn{Type: "address"}
	err = formatting.validateColumns()
	got = fmt.Sprint(err)
	want = "bad encoding and types:\n" +
		"c1: no type specified for encoding: B"
	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted\n%s,\ngot\n%s",
			want, got)
	}
}

func TestValueFormattingSetup(t *testing.T) {
	formatting := newValueFormatting()
	err := formatting.setup(filepath.Join("testdata", t.Name()+".yml"))
	got := fmt.Sprint(err)
	want := "bad encoding and types:\ncol1: no type specified for encoding: B"

	if got != want {
		t.Errorf("Responded incorrectly to bad input:\nwanted %s,\ngot %s",
			want, got)
	}
}

func TestValueFormattingFormat(t *testing.T) {
	formatting := newValueFormatting()
	formatting.settings.ProtocolBufferDefinitions =
		append(formatting.settings.ProtocolBufferDefinitions,
			filepath.Join("testdata", "addressbook.proto"))
	family := newValueFormatFamily()
	family.DefaultEncoding = "Binary"
	formatting.settings.Families["binaries"] = family
	formatting.settings.Families["binaries"].Columns["cb"] =
		valueFormatColumn{Type: "int16"}

	formatting.settings.Columns["hexy"] =
		valueFormatColumn{Encoding: "hex"}
	formatting.settings.Columns["address"] =
		valueFormatColumn{Encoding: "p", Type: "tutorial.Person"}
	formatting.settings.Columns["person"] = valueFormatColumn{Encoding: "p"}
	err := formatting.setup("")
	if err != nil {
		t.Errorf("Error setting up formattting: %v", err)
	}

	got, err := formatting.format("", "f1", "f1:c1", []byte("Hello world!"))
	want := "\"Hello world!\"\n"

	if err != nil {
		t.Errorf("Error during formatting: %v", err)
	}

	if got != want {
		t.Errorf("Values formatted incorrectly: wanted %s, got %s", want, got)
	}

	got, err = formatting.format("  ", "f1", "f1:hexy", []byte("Hello world!"))
	want = "  48 65 6c 6c 6f 20 77 6f 72 6c 64 21\n"
	if err != nil {
		t.Errorf("Error when formatting: %v", err)
	}

	if got != want {
		t.Errorf("Values formatted incorrectly: wanted %s, got %s", want, got)
	}

	got, err = formatting.format(
		"    ", "binaries", "binaries:cb", []byte("Hello world!"))
	want = "    [18533 27756 28448 30575 29292 25633]\n"

	if err != nil {
		t.Errorf("Error formatting binary value: %v", err)
	}
	if got != want {
		t.Errorf("Values formatted incorrectly: wanted %s, got %s", want, got)
	}

	in, err := ioutil.ReadFile(filepath.Join("testdata", "person.bin"))
	want =
		"      name:  \"Jim\"\n" +
			"      id:  42\n" +
			"      email:  \"jim@example.com\"\n" +
			"      phones:  {\n" +
			"        number:  \"555-1212\"\n" +
			"        type:  HOME\n" +
			"      }\n\n"

	if err != nil {
		t.Errorf("Error when reading testdata: %v", err)
	}

	for _, col := range []string{"address", "person"} {
		got, err = formatting.format("      ", "f1", "f1:"+col, in)
		if err != nil {
			t.Errorf("Error formatting data: %v", err)
		}
		if got != want {
			t.Errorf("Values formatted incorrectly: wanted %s, got %s", want,
				got)
		}
	}
}

func TestJSONAndYAML(t *testing.T) {
	globalValueFormatting = newValueFormatting()
	err := globalValueFormatting.setup(filepath.Join("testdata", "cat.yml"))
	if err != nil {
		t.Errorf("Error loading YAML:\n%v", err)
	}

	row := bigtable.Row{
		"f1": {
			bigtable.ReadItem{
				Row:    "r1",
				Column: "f1:json",
				Value:  []byte("{\"name\": \"Brave\", \"age\": 2}"),
			},
		},
	}
	var out bytes.Buffer

	printRow(row, &out)
	got := out.String()
	want := ("----------------------------------------\n" +
		"r1\n" +
		"  f1:json\n" +
		"    age:     2.00\n" +
		"    name:   \"Brave\"")

	timestampsRE := regexp.MustCompile("[ ]+@ [^ \t\n]+")
	got = string(timestampsRE.ReplaceAll([]byte(got), []byte("")))

	if !strings.Contains(got, want) {
		t.Errorf("Formatting printed incorrectly: wanted\n%v\n,\ngot\n%v\n", want, got)
	}
}

func TestProtobufferAndYAML(t *testing.T) {

	globalValueFormatting = newValueFormatting()
	globalValueFormatting.setup(filepath.Join("testdata", "cat.yml"))

	row := bigtable.Row{
		"f1": {
			bigtable.ReadItem{
				Row:    "r1",
				Column: "f1:cat",
				Value:  []byte("\n\x05Brave\x10\x02"),
			},
		},
	}
	var out bytes.Buffer

	printRow(row, &out)
	got := out.String()
	want := ("----------------------------------------\n" +
		"r1\n" +
		"  f1:cat\n" +
		"    name:  \"Brave\"\n" +
		"    age:  2\n")

	timestampsRE := regexp.MustCompile("[ ]+@ [^ \t\n]+")

	stripTimestamps := func(s string) string {
		return string(timestampsRE.ReplaceAll([]byte(s), []byte("")))
	}
	got = stripTimestamps(got)

	if !strings.Contains(got, want) {
		t.Errorf("Formatting printed incorrectly: wanted\n%s\n,\ngot\n%s", want, got)
	}
}

func TestPrintRow(t *testing.T) {
	row := bigtable.Row{
		"f1": {
			bigtable.ReadItem{
				Row:    "r1",
				Column: "f1:c1",
				Value:  []byte("Hello!"),
			},
			bigtable.ReadItem{
				Row:    "r1",
				Column: "f1:c2",
				Value:  []byte{1, 2},
			},
		},
		"f2": {
			bigtable.ReadItem{
				Row:    "r1",
				Column: "f2:person",
				Value: []byte("\n\x03Jim\x10*\x1a\x0fjim@example.com\"" +
					"\x0c\n\x08555-1212\x10\x01"),
			},
		},
	}

	var out bytes.Buffer

	printRow(row, &out)
	got := out.String()
	want :=
		"----------------------------------------\n" +
			"r1\n" +
			"  f1:c1\n" +
			"    \"Hello!\"\n" +
			"  f1:c2\n" +
			"    \"\\x01\\x02\"\n" +
			"  f2:person\n" +
			"    \"\\n\\x03Jim\\x10*\\x1a\\x0fjim@example.com\\\"\\f\\n\\b555-1212\\x10\\x01\"\n" +
			""

	timestampsRE := regexp.MustCompile("[ ]+@ [^ \t\n]+")

	stripTimestamps := func(s string) string {
		return string(timestampsRE.ReplaceAll([]byte(s), []byte("")))
	}
	got = stripTimestamps(got)

	if got != want {
		t.Errorf("Formatting printed incorrectly:\nwanted\n%s,\ngot\n%s",
			want, got)
	}

	oldValueFormatting := globalValueFormatting
	defer func() { globalValueFormatting = oldValueFormatting }()

	globalValueFormatting = newValueFormatting()
	globalValueFormatting.settings.ProtocolBufferDefinitions =
		[]string{filepath.Join("testdata", "addressbook.proto")}
	globalValueFormatting.settings.Columns["c2"] =
		valueFormatColumn{Encoding: "Binary", Type: "int16"}
	globalValueFormatting.settings.Columns["person"] =
		valueFormatColumn{Encoding: "ProtocolBuffer"}
	globalValueFormatting.setup("")

	want = ("----------------------------------------\n" +
		"r1\n" +
		"  f1:c1\n" +
		"    \"Hello!\"\n" +
		"  f1:c2\n" +
		"    258\n" +
		"  f2:person\n" +
		"    name:  \"Jim\"\n" +
		"    id:  42\n" +
		"    email:  \"jim@example.com\"\n" +
		"    phones:  {\n" +
		"      number:  \"555-1212\"\n" +
		"      type:  HOME\n" +
		"    }\n\n" +
		"")

	var out2 bytes.Buffer
	printRow(row, &out2)
	got = stripTimestamps(out2.String())
	if got != want {
		t.Errorf("Formatting printed incorrectly: wanted %s, got %s", want, got)
	}
}

func TestFormatBadColumnNames(t *testing.T) {
	globalValueFormatting = newValueFormatting()
	_, err := globalValueFormatting.format("", "fam", "nofamilynamecolumn", []byte("value not used"))

	if err == nil {
		t.Errorf("Formatter didn't throw error on bad column name")
	}
}
