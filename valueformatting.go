/*
Copyright 2021 Google LLC

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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v2"
)

type valueFormatColumn struct {
	Encoding string
	Type     string
}

type valueFormatFamily struct {
	DefaultEncoding string `yaml:"default_encoding"`
	DefaultType     string `yaml:"default_type"`
	Columns         map[string]valueFormatColumn
}

func newValueFormatFamily() valueFormatFamily { // for tests :)
	family := valueFormatFamily{}
	family.Columns = make(map[string]valueFormatColumn)
	return family
}

type valueFormatSettings struct {
	ProtocolBufferDefinitions []string `yaml:"protocol_buffer_definitions"`
	ProtocolBufferPaths       []string `yaml:"protocol_buffer_paths"`
	DefaultEncoding           string   `yaml:"default_encoding"`
	DefaultType               string   `yaml:"default_type"`
	Columns                   map[string]valueFormatColumn
	Families                  map[string]valueFormatFamily
}

type valueFormatter func([]byte) (string, error)

type valueFormatting struct {
	settings       valueFormatSettings
	pbMessageTypes map[string]*desc.MessageDescriptor
	formatters     map[[2]string]valueFormatter
}

func newValueFormatting() valueFormatting {
	formatting := valueFormatting{}
	formatting.settings.Columns = make(map[string]valueFormatColumn)
	formatting.settings.Families = make(map[string]valueFormatFamily)
	formatting.pbMessageTypes = make(map[string]*desc.MessageDescriptor)
	formatting.formatters = make(map[[2]string]valueFormatter)
	return formatting
}

var globalValueFormatting = newValueFormatting()

func binaryFormatterHelper(
	in []byte,
	byteOrder binary.ByteOrder,
	elemsize int,
	v interface{}) (string, error) {

	if (len(in) % elemsize) != 0 {
		return "", fmt.Errorf(
			"data size, %d, isn't a multiple of element size, %d",
			len(in),
			elemsize,
		)
	}
	var s string
	err := binary.Read(bytes.NewReader(in), byteOrder, v)
	if err == nil {
		s = fmt.Sprint(v)[1:]
		if len(in) == elemsize {
			s = s[1 : len(s)-1]
		}
	}
	return s, err
}

type binaryValueFormatter func([]byte, binary.ByteOrder) (string, error)

var binaryValueFormatters = map[string]binaryValueFormatter{
	"int8": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]int8, len(in))
		return binaryFormatterHelper(in, byteOrder, 2, &v)
	},
	"int16": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]int16, len(in)/2)
		return binaryFormatterHelper(in, byteOrder, 2, &v)
	},
	"int32": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]int32, len(in)/4)
		return binaryFormatterHelper(in, byteOrder, 4, &v)
	},
	"int64": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]int64, len(in)/8)
		return binaryFormatterHelper(in, byteOrder, 8, &v)
	},
	"uint8": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]uint8, len(in))
		return binaryFormatterHelper(in, byteOrder, 2, &v)
	},
	"uint16": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]uint16, len(in)/2)
		return binaryFormatterHelper(in, byteOrder, 2, &v)
	},
	"uint32": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]uint32, len(in)/4)
		return binaryFormatterHelper(in, byteOrder, 4, &v)
	},
	"uint64": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]uint64, len(in)/8)
		return binaryFormatterHelper(in, byteOrder, 8, &v)
	},
	"float32": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]float32, len(in)/4)
		return binaryFormatterHelper(in, byteOrder, 4, &v)
	},
	"float64": func(in []byte, byteOrder binary.ByteOrder) (string, error) {
		v := make([]float64, len(in)/8)
		return binaryFormatterHelper(in, byteOrder, 8, &v)
	},
}

func (f *valueFormatting) binaryFormatter(
	encoding validEncodings, ctype string,
) valueFormatter {
	var byteOrder binary.ByteOrder
	// We don't check the get below because it's checked in
	// validateType, which is called by validateFormat, which is
	// called by format before calling this. :)
	typeFormatter := binaryValueFormatters[ctype]
	if encoding == bigEndian {
		byteOrder = binary.BigEndian
	} else {
		byteOrder = binary.LittleEndian
	}
	return func(in []byte) (string, error) {
		return typeFormatter(in, byteOrder)
	}
}

// jsonFormatter returns a valueFormatter function that pretty-prints JSON values.
func (f *valueFormatting) jsonFormatter() (valueFormatter, error) {
	return func(in []byte) (string, error) {

		var outJSON interface{}
		err := json.Unmarshal(in, &outJSON)
		if err != nil {
			return "", err
		}

		// Recursive inner function that returns strings from
		// JSON values and nested JSON data structures. This forward declaration
		// required to allow the recursion by the function.
		var fmat func(v interface{}, indent string) string
		fmat = func(v interface{}, indent string) string {
			switch t := v.(type) {
			case string:
				return fmt.Sprintf("%s%6q", indent, t)
			case int:
				return fmt.Sprintf("%s%6d", indent, t)
			case float64:
				// TODO: Decide whether floating-point value precision should
				// be configurable
				return fmt.Sprintf("%s%6.2f", indent, t)
			case []interface{}:
				s := fmt.Sprintf("\n%s[\n", indent)
				for _, v := range t {
					s += fmt.Sprintf("%s\n", fmat(v, fmt.Sprintf("  %s", indent)))
				}
				s += fmt.Sprintf("%s]", indent)
				return s
			case map[string]interface{}:

				// Sort the keys first for alphabetical field print order
				var keys []string
				for k := range t {
					keys = append(keys, k)
				}
				sort.Strings(keys)

				s := "\n"
				for _, k := range keys {
					v := t[k]
					fv := fmat(v, fmt.Sprintf("  %s", indent))
					s += fmt.Sprintf("%s%s: %v\n", indent, k, fv)
				}
				return s
			}
			return fmt.Sprintf("%v", v)
		}

		rs := fmat(outJSON, "")
		return strings.TrimLeft(rs, "\n"), nil
	}, nil
}

func (f *valueFormatting) pbFormatter(ctype string) (valueFormatter, error) {
	md := f.pbMessageTypes[strings.ToLower(ctype)]

	if md == nil {
		return nil, fmt.Errorf("no Protocol-Buffer message time for: %v", ctype)
	}
	protoV2md := md.UnwrapMessage()

	return func(in []byte) (string, error) {
		message := dynamicpb.NewMessage(protoV2md)
		err := proto.Unmarshal(in, message)
		if err != nil {
			return "", fmt.Errorf("couldn't deserialize bytes to protobuffer message: %v", err)
		}

		data, err := prototext.MarshalOptions{
			Multiline: true,
			Indent:    "  ",
		}.Marshal(message)
		if err != nil {
			return "", fmt.Errorf("couldn't serialize message to bytes: %v", err)
		}

		return string(data), nil
	}, nil
}

type validEncodings int

const (
	none           validEncodings = 1 << iota // INTERNAL.
	bigEndian                                 // is a list of all the
	littleEndian                              // encodings supported
	protocolBuffer                            // for pretty-print
	hex                                       // formatting
	jsonEncoded
)

var validValueFormattingEncodings = map[string]validEncodings{
	"bigendian":       bigEndian,
	"b":               bigEndian,
	"binary":          bigEndian,
	"hex":             hex,
	"h":               hex,
	"j":               jsonEncoded,
	"json":            jsonEncoded,
	"littleendian":    littleEndian,
	"L":               littleEndian,
	"protocolbuffer":  protocolBuffer,
	"protocol-buffer": protocolBuffer,
	"protocol_buffer": protocolBuffer,
	"proto":           protocolBuffer,
	"p":               protocolBuffer,
	"":                none,
}

func (f *valueFormatting) validateEncoding(encoding string) (validEncodings, error) {
	validEncoding, got := validValueFormattingEncodings[strings.ToLower(encoding)]
	if !got {
		return 0, fmt.Errorf("invalid encoding: %s", encoding)
	}
	return validEncoding, nil
}

func (f *valueFormatting) validateType(
	cname string, validEncoding validEncodings, encoding, ctype string,
) (string, error) {
	var got bool
	switch validEncoding {
	case littleEndian, bigEndian:
		if ctype == "" {
			return ctype, fmt.Errorf(
				"no type specified for encoding: %s",
				encoding)
		}
		_, got = binaryValueFormatters[strings.ToLower(ctype)]
		if !got {
			return ctype, fmt.Errorf("invalid type: %s for encoding: %s",
				ctype, encoding)
		}
		ctype = strings.ToLower(ctype)
	case protocolBuffer:
		if ctype == "" {
			ctype = cname
		}
		_, got = f.pbMessageTypes[strings.ToLower(ctype)]
		if !got {
			return ctype, fmt.Errorf("invalid type: %s for encoding: %s",
				ctype, encoding)
		}
	}
	return ctype, nil
}

func (f *valueFormatting) validateFormat(
	cname, encoding, ctype string,
) (validEncodings, string, error) {
	validEncoding, err := f.validateEncoding(encoding)
	if err == nil {
		ctype, err =
			f.validateType(cname, validEncoding, encoding, ctype)
	}
	return validEncoding, ctype, err
}

func (f *valueFormatting) override(old, new string) string {
	if new != "" {
		return new
	}
	return old
}

func (f *valueFormatting) validateColumns() error {
	defaultEncoding := f.settings.DefaultEncoding
	defaultType := f.settings.DefaultType

	var errs []string
	for cname, col := range f.settings.Columns {
		_, _, err := f.validateFormat(
			cname,
			f.override(defaultEncoding, col.Encoding),
			f.override(defaultType, col.Type))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", cname, err))
		}
	}
	for fname, fam := range f.settings.Families {
		familyEncoding :=
			f.override(defaultEncoding, fam.DefaultEncoding)
		familyType := f.override(defaultType, fam.DefaultType)
		for cname, col := range fam.Columns {
			_, _, err := f.validateFormat(
				cname,
				f.override(familyEncoding, col.Encoding),
				f.override(familyType, col.Type))
			if err != nil {
				errs = append(errs, fmt.Sprintf(
					"%s:%s: %s", fname, cname, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf(
			"bad encoding and types:\n%s",
			strings.Join(errs, "\n"))
	}
	return nil
}

func (f *valueFormatting) parse(path string) error {
	data, err := ioutil.ReadFile(path)
	if err == nil {
		err = yaml.UnmarshalStrict([]byte(data), &f.settings)
	}
	return err
}

func (f *valueFormatting) setupPBMessages() error {
	if len(f.settings.ProtocolBufferDefinitions) > 0 {
		parser := protoparse.Parser{
			ImportPaths: f.settings.ProtocolBufferPaths,
		}
		fds, err := parser.ParseFiles(
			f.settings.ProtocolBufferDefinitions...)
		if err != nil {
			return err
		}
		for _, fd := range fds {
			prefix := fd.GetPackage()
			for _, md := range fd.GetMessageTypes() {
				key := md.GetName()
				f.pbMessageTypes[strings.ToLower(key)] = md
				if prefix != "" {
					key = prefix + "." + key
					f.pbMessageTypes[strings.ToLower(key)] = md
				}
			}
		}
	}
	return nil
}

func (f *valueFormatting) setup(formatFilePath string) error {
	var err error = nil

	if formatFilePath != "" {
		err = f.parse(formatFilePath)
	}

	if err != nil {
		return err
	}

	// call setupPBMessages() and validateColumns() even if
	// format-file is not specified
	err = f.setupPBMessages()
	if err != nil {
		return err
	}

	err = f.validateColumns()
	if err != nil {
		return err
	}
	return nil
}

func (f *valueFormatting) colEncodingType(
	family, column string,
) (string, string) {
	defaultEncoding := f.settings.DefaultEncoding
	defaultType := f.settings.DefaultType

	fam, got := f.settings.Families[family]
	if got {
		familyEncoding :=
			f.override(defaultEncoding, fam.DefaultEncoding)
		familyType := f.override(defaultType, fam.DefaultType)
		col, got := fam.Columns[column]
		if got {
			return f.override(familyEncoding, col.Encoding),
				f.override(familyType, col.Type)
		}
		return familyEncoding, familyType
	}
	col, got := f.settings.Columns[column]
	if got {
		return f.override(defaultEncoding, col.Encoding),
			f.override(defaultType, col.Type)
	}
	return defaultEncoding, defaultType
}

func (f *valueFormatting) badFormatter(err error) valueFormatter {
	return func(in []byte) (string, error) {
		return "", err
	}
}

func (f *valueFormatting) hexFormatter(in []byte) (string, error) {
	return fmt.Sprintf("% x", in), nil
}

func (f *valueFormatting) defaultFormatter(in []byte) (string, error) {
	return fmt.Sprintf("%q", in), nil
}

func (f *valueFormatting) format(
	prefix, family, column string, value []byte,
) (string, error) {
	famcolumn := strings.SplitN(column, ":", 2)
	if len(famcolumn) != 2 {
		return "", fmt.Errorf("column name doesn't include family and column")
	}

	fam, column := famcolumn[0], famcolumn[1]
	if fam != family {
		return "", fmt.Errorf("family, %s, and column family, %s, don't match", family, fam)
	}
	key := [2]string{family, column}
	formatter, got := f.formatters[key]
	if !got {
		encoding, ctype := f.colEncodingType(family, column)
		validEncoding, ctype, err :=
			f.validateFormat(column, string(encoding), ctype)
		if err != nil {
			formatter = f.badFormatter(err)
		} else {
			switch validEncoding {
			case bigEndian, littleEndian:
				formatter = f.binaryFormatter(validEncoding, ctype)
			case hex:
				formatter = f.hexFormatter
			case protocolBuffer:
				formatter, err = f.pbFormatter(ctype)
				// pbFormatter can return an error if underlying input PB is
				// bad
				if err != nil {
					return "", err
				}
			case jsonEncoded:
				formatter, err = f.jsonFormatter()
				if err != nil {
					return "", err
				}
			case none:
				formatter = f.defaultFormatter
			}
		}
		f.formatters[key] = formatter
	}
	formatted, err := formatter(value)
	if err == nil {
		formatted = prefix +
			strings.TrimSuffix(
				strings.Replace(formatted, "\n", "\n"+prefix, 999999),
				prefix) + "\n"
	}
	return formatted, err
}
