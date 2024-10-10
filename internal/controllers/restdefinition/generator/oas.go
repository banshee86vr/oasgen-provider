package generator

import (
	"fmt"
	"strings"

	definitionv1alpha1 "github.com/krateoplatformops/oasgen-provider/apis/restdefinitions/v1alpha1"

	"github.com/krateoplatformops/crdgen"
	"github.com/krateoplatformops/oasgen-provider/internal/tools/generation"
	"github.com/krateoplatformops/oasgen-provider/internal/tools/generator/text"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
)

// var g *OASSchemaGenerator

type OASSchemaGenerator struct {
	specByteSchema   []byte
	statusByteSchema []byte
	secByteSchema    map[string][]byte
}

// GenerateByteSchemas generates the byte schemas for the spec, status and auth schemas. Returns a fatal error and a list of generic errors.
func GenerateByteSchemas(doc *libopenapi.DocumentModel[v3.Document], resource definitionv1alpha1.Resource, identifiers []string) (g *OASSchemaGenerator, fatalError error, errors []error) {
	secByteSchema := make(map[string][]byte)
	var schema *base.Schema
	var err error
	for secSchemaPair := doc.Model.Components.SecuritySchemes.First(); secSchemaPair != nil; secSchemaPair = secSchemaPair.Next() {
		authSchemaName, err := generation.GenerateAuthSchemaName(secSchemaPair.Value())
		if err != nil {
			errors = append(errors, err)
			continue
		}

		secByteSchema[authSchemaName], err = generation.GenerateAuthSchemaFromSecuritySchema(secSchemaPair.Value())
		if err != nil {
			errors = append(errors, err)
			continue
		}
	}

	specByteSchema := make(map[string][]byte)
	for _, verb := range resource.VerbsDescription {
		if strings.EqualFold(verb.Action, "create") {
			path := doc.Model.Paths.PathItems.Value(verb.Path)
			if path == nil {
				return nil, fmt.Errorf("path %s not found", verb.Path), errors
			}
			bodySchema := base.CreateSchemaProxy(&base.Schema{Properties: orderedmap.New[string, *base.SchemaProxy]()})

			ops := path.GetOperations()
			if ops == nil {
				return nil, fmt.Errorf("operations not found for %s", verb.Path), errors
			}

			op := ops.Value(strings.ToLower(verb.Method))
			if op == nil {
				return nil, fmt.Errorf("operation not found for %s", verb.Path), errors
			}
			if op.RequestBody != nil {
				bodySchema = op.RequestBody.Content.Value("application/json").Schema
			}
			if bodySchema == nil {
				return nil, fmt.Errorf("body schema not found for %s", verb.Path), errors
			}
			schema, err = bodySchema.BuildSchema()
			if err != nil {
				return nil, fmt.Errorf("building schema for %s: %w", verb.Path, err), errors
			}
			if len(schema.Type) > 0 {
				if schema.Type[0] == "array" {
					schema.Properties = orderedmap.New[string, *base.SchemaProxy]()
					schema.Properties.Set("items", base.CreateSchemaProxy(
						&base.Schema{
							Type:  []string{"array"},
							Items: schema.Items,
						}))
					schema.Type = []string{"object"}
				}
			}

			populateFromAllOf(schema)
		}

		if len(secByteSchema) > 0 {
			authPair := orderedmap.NewPair("authenticationRefs", base.CreateSchemaProxy(&base.Schema{
				Type:        []string{"object"},
				Description: "AuthenticationRefs represent the reference to a CR containing the authentication information. One authentication method must be set."}))
			req := []string{
				"authenticationRefs",
			}

			if schema == nil {
				om := orderedmap.New[string, *base.SchemaProxy]()
				om.Set(authPair.Key(), authPair.Value())
				schemaproxy := base.CreateSchemaProxy(&base.Schema{
					Type:       []string{"object"},
					Properties: om,
					Required:   req,
				})
				schema = schemaproxy.Schema()
			} else {
				if schema.Properties == nil {
					schema.Properties = orderedmap.New[string, *base.SchemaProxy]()
				}
				schema.Properties.Set(authPair.Key(), authPair.Value())
				schema.Required = req
			}
		}
		for key := range secByteSchema {
			authSchemaProxy := schema.Properties.Value("authenticationRefs")
			if authSchemaProxy == nil {
				return nil, fmt.Errorf("authenticationRefs schema not found for %s", verb.Path), errors
			}

			// Ensure authSchemaProxy.Schema().Properties is initialized
			if authSchemaProxy.Schema().Properties == nil {
				authSchemaProxy.Schema().Properties = orderedmap.New[string, *base.SchemaProxy]()
			}
			authSchemaProxy.Schema().Properties.Set(fmt.Sprintf("%sRef", text.FirstToLower(key)),
				base.CreateSchemaProxy(&base.Schema{Type: []string{"string"}}))
		}

		for _, verb := range resource.VerbsDescription {
			for el := doc.Model.Paths.PathItems.First(); el != nil; el = el.Next() {
				path := el.Value()
				ops := path.GetOperations()
				if ops == nil {
					continue
				}
			}
			path := doc.Model.Paths.PathItems.Value(verb.Path)
			if path == nil {
				return nil, fmt.Errorf("path %s not found", verb.Path), errors
			}
			ops := path.GetOperations()
			if ops == nil {
				continue
			}
			for op := ops.First(); op != nil; op = op.Next() {
				for _, param := range op.Value().Parameters {
					if _, ok := schema.Properties.Get(param.Name); ok {
						errors = append(errors, fmt.Errorf("parameter %s already exists in schema", param.Name))
						continue
					}

					schema.Properties.Set(param.Name, param.Schema)
					schemaProxyParam := schema.Properties.Value(param.Name)
					if schemaProxyParam == nil {
						return nil, fmt.Errorf("schema proxy for %s is nil", param.Name), errors
					}
					schemaParam, err := schemaProxyParam.BuildSchema()
					if err != nil {
						return nil, fmt.Errorf("building schema for %s: %w", verb.Path, err), errors
					}
					schemaParam.Description = fmt.Sprintf("PARAMETER: %s, VERB: %s - %s", param.In, text.CapitaliseFirstLetter(op.Key()), param.Description)
				}
			}
		}

		if schema == nil {
			return nil, fmt.Errorf("schema is nil for %s", verb.Path), errors
		}
		// Add the identifiers to the properties map
		for _, identifier := range identifiers {
			_, ok := schema.Properties.Get(identifier)
			if !ok {
				schema.Properties.Set(identifier, base.CreateSchemaProxy(&base.Schema{
					Description: fmt.Sprintf("IDENTIFIER: %s", identifier),
					Type:        []string{"string"},
				}))
			}
		}

		byteSchema, err := generation.GenerateJsonSchemaFromSchemaProxy(base.CreateSchemaProxy(schema))
		if err != nil {
			return nil, err, errors
		}

		specByteSchema[resource.Kind] = byteSchema
	}

	var statusByteSchema []byte

	// Create an ordered property map
	propMap := orderedmap.New[string, *base.SchemaProxy]()

	// Add the identifiers to the properties map
	for _, identifier := range identifiers {
		propMap.Set(identifier, base.CreateSchemaProxy(&base.Schema{
			Type: []string{"string"},
		}))
	}

	// Create a schema proxy with the properties map
	schemaProxy := base.CreateSchemaProxy(&base.Schema{
		Type:       []string{"object"},
		Properties: propMap,
	})

	statusSchema, err := schemaProxy.BuildSchema()
	if err != nil {
		return nil, fmt.Errorf("building status schema for %s: %w", identifiers, err), errors
	}

	statusByteSchema, err = generation.GenerateJsonSchemaFromSchemaProxy(base.CreateSchemaProxy(statusSchema))
	if err != nil {
		return nil, err, errors
	}

	g = &OASSchemaGenerator{
		specByteSchema:   specByteSchema[resource.Kind],
		statusByteSchema: statusByteSchema,
		secByteSchema:    secByteSchema,
	}

	return g, nil, errors
}

// func PopulateFromAllOf() is a method that populates the schema with the properties from the allOf field.
// the recursive function to populate the schema with the properties from the allOf field.
func populateFromAllOf(schema *base.Schema) {
	if len(schema.Type) > 0 && schema.Type[0] == "array" {
		if schema.Items != nil {
			if schema.Items.N == 0 {
				sch, err := schema.Items.A.BuildSchema()
				if err != nil {
					return
				}

				populateFromAllOf(sch)
			}
		}
		return
	}
	for prop := schema.Properties.First(); prop != nil; prop = prop.Next() {
		populateFromAllOf(prop.Value().Schema())
	}
	for _, proxy := range schema.AllOf {
		propSchema, err := proxy.BuildSchema()
		populateFromAllOf(propSchema)
		if err != nil {
			return
		}
		// Iterate over the properties of the schema with First() and Next()
		for prop := propSchema.Properties.First(); prop != nil; prop = prop.Next() {
			if schema.Properties == nil {
				schema.Properties = orderedmap.New[string, *base.SchemaProxy]()
			}
			// Add the property to the schema
			schema.Properties.Set(prop.Key(), prop.Value())
		}
	}
}

func (g *OASSchemaGenerator) OASSpecJsonSchemaGetter() crdgen.JsonSchemaGetter {
	return &oasSpecJsonSchemaGetter{
		g: g,
	}
}

var _ crdgen.JsonSchemaGetter = (*oasSpecJsonSchemaGetter)(nil)

type oasSpecJsonSchemaGetter struct {
	g *OASSchemaGenerator
}

func (a *oasSpecJsonSchemaGetter) Get() ([]byte, error) {
	return a.g.specByteSchema, nil
}

func (g *OASSchemaGenerator) OASStatusJsonSchemaGetter() crdgen.JsonSchemaGetter {
	return &oasStatusJsonSchemaGetter{
		g: g,
	}
}

var _ crdgen.JsonSchemaGetter = (*oasStatusJsonSchemaGetter)(nil)

type oasStatusJsonSchemaGetter struct {
	g *OASSchemaGenerator
}

func (a *oasStatusJsonSchemaGetter) Get() ([]byte, error) {
	return a.g.statusByteSchema, nil
}

func (g *OASSchemaGenerator) OASAuthJsonSchemaGetter(secSchemaName string) crdgen.JsonSchemaGetter {
	return &oasAuthJsonSchemaGetter{
		g:             g,
		secSchemaName: secSchemaName,
	}
}

var _ crdgen.JsonSchemaGetter = (*oasAuthJsonSchemaGetter)(nil)

type oasAuthJsonSchemaGetter struct {
	g             *OASSchemaGenerator
	secSchemaName string
}

func (a *oasAuthJsonSchemaGetter) Get() ([]byte, error) {
	return a.g.secByteSchema[a.secSchemaName], nil
}

var _ crdgen.JsonSchemaGetter = (*staticJsonSchemaGetter)(nil)

func StaticJsonSchemaGetter() crdgen.JsonSchemaGetter {
	return &staticJsonSchemaGetter{}
}

type staticJsonSchemaGetter struct {
}

func (f *staticJsonSchemaGetter) Get() ([]byte, error) {
	return nil, nil
}