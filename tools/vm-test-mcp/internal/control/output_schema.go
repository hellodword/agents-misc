package control

import (
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func outputSchema(name string) any {
	var t reflect.Type
	switch name {
	case "service_get":
		t = reflect.TypeFor[ServiceView]()
	case "template_list":
		t = reflect.TypeFor[TemplatePage]()
	case "lab_get", "nix_job_get":
		t = reflect.TypeFor[wire.ExecutionView]()
	case "wait":
		t = reflect.TypeFor[WaitResult]()
	default:
		return nil
	}
	schema, err := jsonschema.ForType(t, nil)
	if err != nil {
		panic(err)
	}
	return schema
}
