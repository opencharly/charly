// Command wiregen makes CUE the author of record for Charly's protobuf
// transport contract. The committed .proto files and language bindings are
// generated artifacts; they are never authoring inputs after the bootstrap
// import has completed.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	protobuf "github.com/emicklei/proto"
)

type protocolModel struct {
	Syntax    string         `json:"syntax"`
	Package   string         `json:"package"`
	GoPackage string         `json:"go_package"`
	Doc       string         `json:"doc,omitempty"`
	Messages  []messageModel `json:"messages"`
	Services  []serviceModel `json:"services"`
}

type messageModel struct {
	Name          string       `json:"name"`
	Doc           string       `json:"doc,omitempty"`
	Fields        []fieldModel `json:"fields,omitempty"`
	ReservedNames []string     `json:"reserved_names,omitempty"`
	ReservedNums  []int        `json:"reserved_numbers,omitempty"`
}

type fieldModel struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Number     int    `json:"number"`
	Doc        string `json:"doc,omitempty"`
	Repeated   bool   `json:"repeated,omitempty"`
	Optional   bool   `json:"optional,omitempty"`
	MapKeyType string `json:"map_key_type,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

type serviceModel struct {
	Name    string        `json:"name"`
	Doc     string        `json:"doc,omitempty"`
	Methods []methodModel `json:"methods"`
}

type methodModel struct {
	Name            string `json:"name"`
	Request         string `json:"request"`
	Response        string `json:"response"`
	Doc             string `json:"doc,omitempty"`
	ClientStreaming bool   `json:"client_streaming,omitempty"`
	ServerStreaming bool   `json:"server_streaming,omitempty"`
	Deprecated      bool   `json:"deprecated,omitempty"`
}

func main() {
	schemaDir := flag.String("schema", "protocol/schema", "directory containing the CUE protocol model")
	out := flag.String("out", "proto/plugin.proto", "output path")
	flag.Parse()

	if err := generate(*schemaDir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "wiregen:", err)
		os.Exit(1)
	}
}

func generate(schemaDir, out string) error {
	model, err := loadModel(schemaDir)
	if err != nil {
		return err
	}
	if err := validateModel(&model); err != nil {
		return err
	}
	data := renderProto(&model)
	return writeIfChanged(out, data)
}

func loadModel(schemaDir string) (protocolModel, error) {
	abs, err := filepath.Abs(schemaDir)
	if err != nil {
		return protocolModel{}, err
	}
	instances := load.Instances([]string{"."}, &load.Config{Dir: abs})
	if len(instances) != 1 {
		return protocolModel{}, fmt.Errorf("load %s: expected one CUE instance, got %d", schemaDir, len(instances))
	}
	ctx := cuecontext.New()
	value := ctx.BuildInstance(instances[0])
	if err := value.Validate(cue.Concrete(true)); err != nil {
		return protocolModel{}, fmt.Errorf("validate CUE protocol model: %w", err)
	}
	protocol := value.LookupPath(cue.ParsePath("protocol"))
	if !protocol.Exists() {
		return protocolModel{}, errors.New("CUE protocol model does not define concrete field protocol")
	}
	var model protocolModel
	if err := protocol.Decode(&model); err != nil {
		return protocolModel{}, fmt.Errorf("decode CUE protocol model: %w", err)
	}
	return model, nil
}

func modelFromProto(parsed *protobuf.Proto) (protocolModel, error) {
	model := protocolModel{Syntax: "proto3"}
	for _, element := range parsed.Elements {
		switch item := element.(type) {
		case *protobuf.Syntax:
			model.Syntax = item.Value
		case *protobuf.Package:
			model.Package = item.Name
		case *protobuf.Option:
			if item.Name == "go_package" {
				model.GoPackage = item.Constant.Source
			}
		case *protobuf.Message:
			message, err := messageFromProto(item)
			if err != nil {
				return protocolModel{}, err
			}
			model.Messages = append(model.Messages, message)
		case *protobuf.Service:
			service, err := serviceFromProto(item)
			if err != nil {
				return protocolModel{}, err
			}
			model.Services = append(model.Services, service)
		case *protobuf.Comment:
			// File comments are intentionally not part of the wire descriptor.
		default:
			return protocolModel{}, fmt.Errorf("bootstrap importer: unsupported top-level protobuf element %T", element)
		}
	}
	return model, nil
}

func messageFromProto(input *protobuf.Message) (messageModel, error) {
	message := messageModel{Name: input.Name, Doc: commentText(input.Comment, nil)}
	for _, element := range input.Elements {
		switch item := element.(type) {
		case *protobuf.NormalField:
			message.Fields = append(message.Fields, fieldModel{
				Name: item.Name, Type: item.Type, Number: item.Sequence,
				Doc: commentText(item.Comment, item.InlineComment), Repeated: item.Repeated,
				Optional: item.Optional, Deprecated: item.IsDeprecated(),
			})
		case *protobuf.MapField:
			message.Fields = append(message.Fields, fieldModel{
				Name: item.Name, Type: item.Type, Number: item.Sequence,
				Doc: commentText(item.Comment, item.InlineComment), MapKeyType: item.KeyType,
				Deprecated: item.IsDeprecated(),
			})
		case *protobuf.Reserved:
			message.ReservedNames = append(message.ReservedNames, item.FieldNames...)
			for _, r := range item.Ranges {
				if r.From != r.To {
					return messageModel{}, fmt.Errorf("message %s: ranged reserved numbers are not yet supported", input.Name)
				}
				message.ReservedNums = append(message.ReservedNums, r.From)
			}
		case *protobuf.Comment:
		default:
			return messageModel{}, fmt.Errorf("message %s: unsupported protobuf element %T", input.Name, element)
		}
	}
	return message, nil
}

func serviceFromProto(input *protobuf.Service) (serviceModel, error) {
	service := serviceModel{Name: input.Name, Doc: commentText(input.Comment, nil)}
	for _, element := range input.Elements {
		switch item := element.(type) {
		case *protobuf.RPC:
			service.Methods = append(service.Methods, methodModel{
				Name: item.Name, Request: item.RequestType, Response: item.ReturnsType,
				Doc:             commentText(item.Comment, item.InlineComment),
				ClientStreaming: item.StreamsRequest, ServerStreaming: item.StreamsReturns,
			})
		case *protobuf.Comment:
		default:
			return serviceModel{}, fmt.Errorf("service %s: unsupported protobuf element %T", input.Name, element)
		}
	}
	return service, nil
}

// commentText joins the FULL comment block attached to an element. emicklei/
// proto merges a run of consecutive // lines into one Comment whose Lines hold
// every line, but Comment.Message() returns only Lines[0] — using it here
// silently truncated every multi-line doc to its first line (the bootstrap
// defect that cut design rationale out of protocol/schema/plugin.cue).
func commentText(leading, inline *protobuf.Comment) string {
	var parts []string
	for _, comment := range []*protobuf.Comment{leading, inline} {
		if comment == nil {
			continue
		}
		for _, line := range comment.Lines {
			parts = append(parts, strings.TrimSpace(line))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func validateModel(model *protocolModel) error {
	if model.Syntax != "proto3" {
		return fmt.Errorf("syntax must be proto3, got %q", model.Syntax)
	}
	if model.Package == "" || model.GoPackage == "" {
		return errors.New("package and go_package are required")
	}
	messageNames, err := validateMessages(model.Messages)
	if err != nil {
		return err
	}
	return validateServices(model.Services, messageNames)
}

func validateMessages(messages []messageModel) (map[string]bool, error) {
	messageNames := map[string]bool{}
	for _, message := range messages {
		if message.Name == "" || messageNames[message.Name] {
			return nil, fmt.Errorf("duplicate or empty message name %q", message.Name)
		}
		messageNames[message.Name] = true
	}
	scalars := map[string]bool{
		"double": true, "float": true, "int32": true, "int64": true,
		"uint32": true, "uint64": true, "sint32": true, "sint64": true,
		"fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true,
		"bool": true, "string": true, "bytes": true,
	}
	for _, message := range messages {
		names, numbers := map[string]bool{}, map[int]bool{}
		reservedNames, reservedNumbers := map[string]bool{}, map[int]bool{}
		for _, name := range message.ReservedNames {
			reservedNames[name] = true
		}
		for _, number := range message.ReservedNums {
			reservedNumbers[number] = true
		}
		for _, field := range message.Fields {
			if field.Name == "" || names[field.Name] || reservedNames[field.Name] {
				return nil, fmt.Errorf("message %s: duplicate, empty, or reserved field name %q", message.Name, field.Name)
			}
			if field.Number <= 0 || field.Number >= 1<<29 || numbers[field.Number] || reservedNumbers[field.Number] {
				return nil, fmt.Errorf("message %s: invalid, duplicate, or reserved field number %d", message.Name, field.Number)
			}
			if !scalars[field.Type] && !messageNames[field.Type] {
				return nil, fmt.Errorf("message %s field %s: unknown type %q", message.Name, field.Name, field.Type)
			}
			if field.MapKeyType != "" && !mapKeyScalar(field.MapKeyType) {
				return nil, fmt.Errorf("message %s field %s: invalid map key type %q", message.Name, field.Name, field.MapKeyType)
			}
			if field.MapKeyType != "" && (field.Repeated || field.Optional) {
				return nil, fmt.Errorf("message %s field %s: map cannot be repeated or optional", message.Name, field.Name)
			}
			names[field.Name], numbers[field.Number] = true, true
		}
	}
	return messageNames, nil
}

func validateServices(services []serviceModel, messageNames map[string]bool) error {
	serviceNames := map[string]bool{}
	for _, service := range services {
		if service.Name == "" || serviceNames[service.Name] {
			return fmt.Errorf("duplicate or empty service name %q", service.Name)
		}
		serviceNames[service.Name] = true
		methods := map[string]bool{}
		for _, method := range service.Methods {
			if method.Name == "" || methods[method.Name] {
				return fmt.Errorf("service %s: duplicate or empty method %q", service.Name, method.Name)
			}
			if !messageNames[method.Request] || !messageNames[method.Response] {
				return fmt.Errorf("service %s method %s: request/response must name declared messages", service.Name, method.Name)
			}
			methods[method.Name] = true
		}
	}
	return nil
}

func mapKeyScalar(value string) bool {
	switch value {
	case "int32", "int64", "uint32", "uint64", "sint32", "sint64", "fixed32", "fixed64", "sfixed32", "sfixed64", "bool", "string":
		return true
	default:
		return false
	}
}

func renderProto(model *protocolModel) []byte {
	var out bytes.Buffer
	out.WriteString("// Code generated by sdk/internal/wiregen from protocol/schema/*.cue. DO NOT EDIT.\n")
	out.WriteString("// CUE is the authoritative source for this transport contract.\n\n")
	writeDoc(&out, model.Doc, "")
	fmt.Fprintf(&out, "syntax = %q;\npackage %s;\noption go_package = %q;\n\n", model.Syntax, model.Package, model.GoPackage)
	for _, message := range model.Messages {
		writeDoc(&out, message.Doc, "")
		fmt.Fprintf(&out, "message %s {\n", message.Name)
		if len(message.ReservedNames) > 0 {
			names := append([]string(nil), message.ReservedNames...)
			sort.Strings(names)
			quoted := make([]string, len(names))
			for i, name := range names {
				quoted[i] = strconv.Quote(name)
			}
			fmt.Fprintf(&out, "  reserved %s;\n", strings.Join(quoted, ", "))
		}
		if len(message.ReservedNums) > 0 {
			numbers := append([]int(nil), message.ReservedNums...)
			sort.Ints(numbers)
			parts := make([]string, len(numbers))
			for i, number := range numbers {
				parts[i] = strconv.Itoa(number)
			}
			fmt.Fprintf(&out, "  reserved %s;\n", strings.Join(parts, ", "))
		}
		for _, field := range message.Fields {
			writeDoc(&out, field.Doc, "  ")
			qualifier := ""
			fieldType := field.Type
			switch {
			case field.MapKeyType != "":
				fieldType = fmt.Sprintf("map<%s, %s>", field.MapKeyType, field.Type)
			case field.Repeated:
				qualifier = "repeated "
			case field.Optional:
				qualifier = "optional "
			}
			options := ""
			if field.Deprecated {
				options = " [deprecated = true]"
			}
			fmt.Fprintf(&out, "  %s%s %s = %d%s;\n", qualifier, fieldType, field.Name, field.Number, options)
		}
		out.WriteString("}\n\n")
	}
	for _, service := range model.Services {
		writeDoc(&out, service.Doc, "")
		fmt.Fprintf(&out, "service %s {\n", service.Name)
		for _, method := range service.Methods {
			writeDoc(&out, method.Doc, "  ")
			request, response := method.Request, method.Response
			if method.ClientStreaming {
				request = "stream " + request
			}
			if method.ServerStreaming {
				response = "stream " + response
			}
			fmt.Fprintf(&out, "  rpc %s(%s) returns (%s);\n", method.Name, request, response)
		}
		out.WriteString("}\n\n")
	}
	return append(bytes.TrimRight(out.Bytes(), "\n"), '\n')
}

// writeDoc renders a doc string as a block of // comment lines. Internal
// blank lines are preserved as bare // lines so paragraph breaks survive the
// round trip; only the block's leading/trailing whitespace is trimmed. An
// empty doc renders nothing at all.
func writeDoc(out io.Writer, doc, indent string) {
	block := strings.TrimSpace(doc)
	if block == "" {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			fmt.Fprintf(out, "%s// %s\n", indent, trimmed)
		} else {
			fmt.Fprintf(out, "%s//\n", indent)
		}
	}
}

func writeIfChanged(path string, data []byte) (returnErr error) {
	if strings.HasSuffix(path, ".go") {
		formatted, err := format.Source(data)
		if err != nil {
			return err
		}
		data = formatted
	}
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wiregen-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary generated wire file: %w", err))
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Chmod(0o644); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
