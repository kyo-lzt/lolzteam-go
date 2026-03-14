package codegen

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// OpenAPI model (only the bits we need)
// ---------------------------------------------------------------------------

type OpenAPISpec struct {
	Paths      map[string]PathItem `json:"paths"`
	Components Components          `json:"components"`
}

type Components struct {
	Schemas    map[string]SchemaObj   `json:"schemas"`
	Parameters map[string]ParamObj   `json:"parameters"`
	Responses  map[string]ResponseObj `json:"responses"`
}

type PathItem map[string]json.RawMessage // method → operation JSON

type OperationObj struct {
	OperationID string            `json:"operationId"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Parameters  []json.RawMessage `json:"parameters"`
	RequestBody *RequestBodyObj   `json:"requestBody"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

type RequestBodyObj struct {
	Required bool                    `json:"required"`
	Content  map[string]MediaTypeObj `json:"content"`
}

type MediaTypeObj struct {
	Schema json.RawMessage `json:"schema"`
}

type ResponseObj struct {
	Content map[string]MediaTypeObj `json:"content"`
}

type ParamObj struct {
	Ref         string    `json:"$ref"`
	Name        string    `json:"name"`
	In          string    `json:"in"`
	Required    bool      `json:"required"`
	Schema      SchemaObj `json:"schema"`
	Description string    `json:"description"`
}

type SchemaObj struct {
	Ref                  string               `json:"$ref"`
	Type                 json.RawMessage      `json:"type"` // string or []string
	Properties           map[string]SchemaObj `json:"properties"`
	Items                *SchemaObj           `json:"items"`
	Required             []string             `json:"required"`
	OneOf                []SchemaObj          `json:"oneOf"`
	AnyOf                []SchemaObj          `json:"anyOf"`
	AllOf                []SchemaObj          `json:"allOf"`
	Enum                 []json.RawMessage    `json:"enum"`
	AdditionalProperties *SchemaObj           `json:"additionalProperties"`
	Description          string               `json:"description"`
	Title                string               `json:"title"`
	Default              json.RawMessage      `json:"default"`
	Format               string               `json:"format"`
}

// ---------------------------------------------------------------------------
// Parsed types used by the generator
// ---------------------------------------------------------------------------

type GoType struct {
	Name      string     // Go type expression, e.g. "int64", "ForumThreadModel"
	IsPtr     bool       // wrap in *
	StructDef *StructDef // inline struct definition, nil if named/primitive
}

type StructDef struct {
	Name   string
	Fields []StructField
}

type StructField struct {
	Name    string
	Type    GoType
	Tag     string
	Comment string
}

type Operation struct {
	Group          string
	Method         string
	HTTPMethod     string
	Path           string
	Summary        string
	PathParams     []PathParam
	QueryParams    []QueryParam
	BodyProps      []BodyProp
	HasBinaryBody  bool
	IsArrayBody    bool       // body schema is type: "array" (e.g. batch endpoints)
	ArrayItemProps []BodyProp // properties of the array item object
	ResponseSchema SchemaObj
	HasResponse    bool
}

type PathParam struct {
	Name      string
	GoName    string
	GoType    string
	SchemaObj SchemaObj
}

type QueryParam struct {
	Name     string
	GoName   string
	GoType   string
	Required bool
	IsArray  bool
	ItemType string
}

type BodyProp struct {
	Name     string
	GoName   string
	GoType   string
	Required bool
	IsBinary bool
}

// ---------------------------------------------------------------------------
// Generator context
// ---------------------------------------------------------------------------

type Generator struct {
	Spec       OpenAPISpec
	Prefix     string // "Forum" or "Market"
	RawSpec    map[string]json.RawMessage
	Resolving  map[string]bool // cycle detection
	NamedTypes map[string]*StructDef
	Operations []Operation
	Groups     map[string][]Operation
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

func (g *Generator) ParseOperations() {
	for path, methods := range g.Spec.Paths {
		for httpMethod, rawOp := range methods {
			if httpMethod == "parameters" {
				continue
			}

			var op OperationObj
			if err := json.Unmarshal(rawOp, &op); err != nil {
				continue
			}
			if op.OperationID == "" {
				continue
			}

			group, method := SplitOperationID(op.OperationID)
			// Normalize known typos in group names
			if group == "Manging" {
				group = "Managing"
			}
			parsed := Operation{
				Group:      group,
				Method:     method,
				HTTPMethod: strings.ToUpper(httpMethod),
				Path:       path,
				Summary:    op.Summary,
			}

			// Parse parameters
			for _, rawParam := range op.Parameters {
				param := g.ResolveParam(rawParam)
				if param.In == "path" {
					pp := PathParam{
						Name:   param.Name,
						GoName: ToPascalCase(param.Name),
						GoType: g.SchemaToGoType(param.Schema),
					}
					parsed.PathParams = append(parsed.PathParams, pp)
				} else if param.In == "query" {
					qp := QueryParam{
						Name:     param.Name,
						GoName:   ToPascalCase(param.Name),
						Required: param.Required,
					}
					resolved := g.ResolveSchemaRef(param.Schema)
					typeStrs := SchemaTypeStrings(resolved)
					if slices.Contains(typeStrs, "array") && resolved.Items != nil {
						qp.IsArray = true
						itemResolved := g.ResolveSchemaRef(*resolved.Items)
						qp.ItemType = g.PrimitiveGoType(itemResolved)
						qp.GoType = "[]" + qp.ItemType
					} else if slices.Contains(typeStrs, "object") && resolved.AdditionalProperties != nil {
						// deepObject params like hours_played: map[string]int64
						valType := g.PrimitiveGoType(g.ResolveSchemaRef(*resolved.AdditionalProperties))
						qp.GoType = "map[string]" + valType
					} else {
						qp.GoType = g.PrimitiveGoType(resolved)
					}
					parsed.QueryParams = append(parsed.QueryParams, qp)
				}
			}

			// Parse request body
			if op.RequestBody != nil {
				bodySchema, contentType := g.ExtractBodySchema(op.RequestBody)
				isMultipart := contentType == "multipart/form-data"
				resolved := g.ResolveSchemaRef(bodySchema)

				// Check if body schema is an array type (e.g. batch endpoints)
				bodyTypeStrs := SchemaTypeStrings(resolved)
				if slices.Contains(bodyTypeStrs, "array") && resolved.Items != nil {
					parsed.IsArrayBody = true
					itemResolved := g.ResolveSchemaRef(*resolved.Items)
					if len(itemResolved.AllOf) > 0 {
						itemResolved = g.MergeAllOfSchemas(itemResolved)
					}
					reqFields := ToSet(itemResolved.Required)
					for propName, propSchema := range itemResolved.Properties {
						goType := g.ResolveGoType(propSchema)
						bp := BodyProp{
							Name:     propName,
							GoName:   ToPascalCase(propName),
							GoType:   goType,
							Required: reqFields[propName],
						}
						parsed.ArrayItemProps = append(parsed.ArrayItemProps, bp)
					}
					sort.Slice(parsed.ArrayItemProps, func(i, j int) bool {
						return parsed.ArrayItemProps[i].Name < parsed.ArrayItemProps[j].Name
					})
				} else if parsed.HTTPMethod == "GET" {
					// GET requests shouldn't have bodies — treat body properties as query params
					if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
						resolved = g.MergeOneOfSchemas(resolved)
					}
					if len(resolved.AllOf) > 0 {
						resolved = g.MergeAllOfSchemas(resolved)
					}
					for propName, propSchema := range resolved.Properties {
						resolvedProp := g.ResolveSchemaRef(propSchema)
						qp := QueryParam{
							Name:     propName,
							GoName:   ToPascalCase(propName),
							Required: false,
						}
						typeStrs := SchemaTypeStrings(resolvedProp)
						if slices.Contains(typeStrs, "array") && resolvedProp.Items != nil {
							qp.IsArray = true
							itemResolved := g.ResolveSchemaRef(*resolvedProp.Items)
							qp.ItemType = g.PrimitiveGoType(itemResolved)
							qp.GoType = "[]" + qp.ItemType
						} else {
							qp.GoType = g.PrimitiveGoType(resolvedProp)
						}
						parsed.QueryParams = append(parsed.QueryParams, qp)
					}
				} else {
					// If oneOf/anyOf, merge all properties
					if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
						resolved = g.MergeOneOfSchemas(resolved)
					}
					if len(resolved.AllOf) > 0 {
						resolved = g.MergeAllOfSchemas(resolved)
					}
					reqFields := ToSet(resolved.Required)
					for propName, propSchema := range resolved.Properties {
						resolvedProp := g.ResolveSchemaRef(propSchema)
						isBinary := isMultipart && resolvedProp.Format == "binary"
						goType := g.ResolveGoType(propSchema)
						if isBinary {
							goType = "FileUpload"
						}
						bp := BodyProp{
							Name:     propName,
							GoName:   ToPascalCase(propName),
							GoType:   goType,
							Required: reqFields[propName],
							IsBinary: isBinary,
						}
						parsed.BodyProps = append(parsed.BodyProps, bp)
						if isBinary {
							parsed.HasBinaryBody = true
						}
					}
					sort.Slice(parsed.BodyProps, func(i, j int) bool {
						return parsed.BodyProps[i].Name < parsed.BodyProps[j].Name
					})
				}
			}

			// Parse response
			parsed.HasResponse = false
			for code, rawResp := range op.Responses {
				if code != "200" && code != "201" {
					continue
				}
				respSchema := g.ExtractResponseSchema(rawResp)
				if respSchema.Ref != "" || len(respSchema.Properties) > 0 || respSchema.Items != nil {
					parsed.ResponseSchema = respSchema
					parsed.HasResponse = true
					break
				}
			}

			g.Operations = append(g.Operations, parsed)
			g.Groups[group] = append(g.Groups[group], parsed)
		}
	}

	// Sort operations within each group for deterministic output
	for group := range g.Groups {
		sort.Slice(g.Groups[group], func(i, j int) bool {
			return g.Groups[group][i].Method < g.Groups[group][j].Method
		})
	}
}

func (g *Generator) ResolveParam(raw json.RawMessage) ParamObj {
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := RefName(ref.Ref)
		if p, ok := g.Spec.Components.Parameters[name]; ok {
			return p
		}
	}
	var param ParamObj
	json.Unmarshal(raw, &param)
	return param
}

func (g *Generator) ExtractBodySchema(rb *RequestBodyObj) (SchemaObj, string) {
	for _, contentType := range []string{"application/json", "multipart/form-data", "application/x-www-form-urlencoded"} {
		if mt, ok := rb.Content[contentType]; ok {
			var schema SchemaObj
			json.Unmarshal(mt.Schema, &schema)
			return schema, contentType
		}
	}
	return SchemaObj{}, ""
}

func (g *Generator) ExtractResponseSchema(raw json.RawMessage) SchemaObj {
	// Check if it's a $ref
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := RefName(ref.Ref)
		if resp, ok := g.Spec.Components.Responses[name]; ok {
			return g.ExtractResponseSchemaFromObj(resp)
		}
	}
	var resp ResponseObj
	json.Unmarshal(raw, &resp)
	return g.ExtractResponseSchemaFromObj(resp)
}

func (g *Generator) ExtractResponseSchemaFromObj(resp ResponseObj) SchemaObj {
	if mt, ok := resp.Content["application/json"]; ok {
		var schema SchemaObj
		json.Unmarshal(mt.Schema, &schema)
		return schema
	}
	return SchemaObj{}
}

func (g *Generator) ResolveSchemaRef(s SchemaObj) SchemaObj {
	if s.Ref == "" {
		return s
	}

	ref := s.Ref
	if !strings.HasPrefix(ref, "#/") {
		return s
	}

	// Standard: #/components/schemas/Name
	name := RefName(ref)
	if schema, ok := g.Spec.Components.Schemas[name]; ok {
		return schema
	}

	// Deep ref like #/components/parameters/random_proxy/schema
	// Navigate the raw JSON tree
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current json.RawMessage
	if raw, ok := g.RawSpec[parts[0]]; ok {
		current = raw
	} else {
		return s
	}

	for _, part := range parts[1:] {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			return s
		}
		next, ok := obj[part]
		if !ok {
			return s
		}
		current = next
	}

	var resolved SchemaObj
	if err := json.Unmarshal(current, &resolved); err != nil {
		return s
	}
	return resolved
}

func (g *Generator) ResolveGoType(s SchemaObj) string {
	if s.Ref != "" {
		name := RefName(s.Ref)
		goName := ToPascalCase(name)
		// If this is a named struct type we generated, use that name
		if _, exists := g.NamedTypes[goName]; exists {
			return goName
		}
		// Otherwise resolve and return the primitive/computed Go type
		resolved := g.ResolveSchemaRef(s)
		return g.SchemaToGoType(resolved)
	}
	resolved := g.ResolveSchemaRef(s)
	return g.SchemaToGoType(resolved)
}

func (g *Generator) SchemaToGoType(s SchemaObj) string {
	if s.Ref != "" {
		name := RefName(s.Ref)
		resolved := g.ResolveSchemaRef(s)
		// Check if it's a simple scalar type (enum, primitive)
		typeStrs := SchemaTypeStrings(resolved)
		if len(typeStrs) == 1 {
			t := typeStrs[0]
			switch t {
			case "string":
				return "string"
			case "integer":
				return "int64"
			case "number":
				return "float64"
			case "boolean":
				return "bool"
			}
		}
		// Multi-type union like ["string", "integer"]
		if len(typeStrs) == 2 {
			return "any"
		}
		return ToPascalCase(name)
	}

	typeStrs := SchemaTypeStrings(s)

	// Multi-type union like ["string", "integer"]
	if len(typeStrs) > 1 {
		return "any"
	}

	if len(typeStrs) == 1 {
		switch typeStrs[0] {
		case "string":
			return "string"
		case "integer":
			return "int64"
		case "number":
			return "float64"
		case "boolean":
			return "bool"
		case "array":
			if s.Items != nil {
				itemType := g.ResolveGoType(*s.Items)
				return "[]" + itemType
			}
			return "[]any"
		case "object":
			if len(s.Properties) > 0 {
				return "any" // inline objects in type context → any
			}
			if s.AdditionalProperties != nil {
				valType := g.ResolveGoType(*s.AdditionalProperties)
				return "map[string]" + valType
			}
			return "map[string]any"
		}
	}

	// oneOf / anyOf / allOf
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return "any"
	}
	if len(s.AllOf) > 0 {
		return "any"
	}

	// No type specified
	return "any"
}

func (g *Generator) PrimitiveGoType(s SchemaObj) string {
	typeStrs := SchemaTypeStrings(s)
	if len(typeStrs) > 1 {
		return "any"
	}
	if len(typeStrs) == 1 {
		switch typeStrs[0] {
		case "string":
			return "string"
		case "integer":
			return "int64"
		case "number":
			return "float64"
		case "boolean":
			return "bool"
		}
	}
	if s.Ref != "" {
		resolved := g.ResolveSchemaRef(s)
		return g.PrimitiveGoType(resolved)
	}
	return "string"
}

func (g *Generator) MergeOneOfSchemas(s SchemaObj) SchemaObj {
	merged := SchemaObj{
		Properties: make(map[string]SchemaObj),
	}
	reqSets := []map[string]bool{}
	variants := s.OneOf
	if len(variants) == 0 {
		variants = s.AnyOf
	}
	for _, variant := range variants {
		resolved := g.ResolveSchemaRef(variant)
		if len(resolved.AllOf) > 0 {
			resolved = g.MergeAllOfSchemas(resolved)
		}
		for k, v := range resolved.Properties {
			if _, exists := merged.Properties[k]; !exists {
				merged.Properties[k] = v
			}
		}
		reqSets = append(reqSets, ToSet(resolved.Required))
	}
	// A field is required only if required in ALL variants
	for propName := range merged.Properties {
		allReq := true
		for _, rs := range reqSets {
			if !rs[propName] {
				allReq = false
				break
			}
		}
		if allReq {
			merged.Required = append(merged.Required, propName)
		}
	}
	return merged
}

func (g *Generator) MergeAllOfSchemas(s SchemaObj) SchemaObj {
	merged := SchemaObj{
		Properties: make(map[string]SchemaObj),
	}
	for _, part := range s.AllOf {
		resolved := g.ResolveSchemaRef(part)
		for k, v := range resolved.Properties {
			merged.Properties[k] = v
		}
		merged.Required = append(merged.Required, resolved.Required...)
	}
	return merged
}

// ---------------------------------------------------------------------------
// Named type generation from component schemas
// ---------------------------------------------------------------------------

func (g *Generator) GenerateNamedTypes() {
	for name, schema := range g.Spec.Components.Schemas {
		goName := ToPascalCase(name)
		typeStrs := SchemaTypeStrings(schema)

		// Skip simple scalars / enums — they map to primitive Go types
		if len(typeStrs) == 1 {
			switch typeStrs[0] {
			case "string", "integer", "number", "boolean":
				continue
			}
		}
		if len(typeStrs) > 1 {
			continue // union type like ["string", "integer"]
		}

		if len(schema.Properties) == 0 {
			continue
		}

		sd := g.SchemaToStructDef(goName, schema)
		g.NamedTypes[goName] = sd
	}
}

func (g *Generator) SchemaToStructDef(name string, s SchemaObj) *StructDef {
	if g.Resolving[name] {
		return &StructDef{Name: name}
	}
	g.Resolving[name] = true
	defer delete(g.Resolving, name)

	sd := &StructDef{Name: name}
	reqSet := ToSet(s.Required)

	propNames := SortedKeys(s.Properties)
	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		goFieldName := ToPascalCase(propName)
		goType := g.ResolveGoType(propSchema)

		// Inline objects → generate as nested struct type or map
		resolved := g.ResolveSchemaRef(propSchema)
		if resolved.Ref == "" && len(resolved.Properties) > 0 {
			inlineName := name + goFieldName
			inlineSD := g.SchemaToStructDef(inlineName, resolved)
			g.NamedTypes[inlineName] = inlineSD
			goType = inlineName
		}

		isRequired := reqSet[propName]
		tagVal := propName + ",omitempty"
		tag := fmt.Sprintf("`json:\"%s\"`", tagVal)

		field := StructField{
			Name: goFieldName,
			Tag:  tag,
		}

		if isRequired {
			field.Type = GoType{Name: goType}
		} else {
			field.Type = GoType{Name: goType, IsPtr: IsPtrCandidate(goType)}
		}

		sd.Fields = append(sd.Fields, field)
	}
	return sd
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func SplitOperationID(opID string) (group, method string) {
	idx := strings.Index(opID, ".")
	if idx < 0 {
		return ToPascalCase(opID), opID
	}
	group = ToPascalCase(opID[:idx])
	rest := opID[idx+1:]
	// Join remaining dots: "Poll.Vote" → "PollVote"
	parts := strings.Split(rest, ".")
	var methodParts []string
	for _, p := range parts {
		methodParts = append(methodParts, ToPascalCase(p))
	}
	method = strings.Join(methodParts, "")
	return group, method
}

func ToPascalCase(s string) string {
	// Strip non-alphanumeric chars (except _ and -)
	var cleaned strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	s = cleaned.String()

	parts := SplitWords(s)
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		upper := strings.ToUpper(part)
		// Common acronyms
		switch upper {
		case "ID", "URL", "URI", "API", "HTML", "JSON", "XML", "CSS", "IP",
			"HTTP", "HTTPS", "SA", "SDA", "AI", "OK", "EA", "VPN", "2FA", "FA":
			result.WriteString(upper)
		default:
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			result.WriteString(string(runes))
		}
	}
	out := result.String()
	// Ensure the identifier starts with a letter (prefix _ for numeric starts)
	if len(out) > 0 && unicode.IsDigit(rune(out[0])) {
		out = "N" + out
	}
	return out
}

func SplitWords(s string) []string {
	// Split on underscores, hyphens, and camelCase boundaries
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	var result []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		// Split camelCase
		var word strings.Builder
		for i, r := range p {
			if i > 0 && unicode.IsUpper(r) {
				result = append(result, word.String())
				word.Reset()
			}
			word.WriteRune(r)
		}
		if word.Len() > 0 {
			result = append(result, word.String())
		}
	}
	return result
}

var GoKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

func CamelCase(s string) string {
	if len(s) == 0 {
		return s
	}
	// Lowercase the first letter/acronym
	runes := []rune(s)
	i := 0
	for i < len(runes) && unicode.IsUpper(runes[i]) {
		i++
	}
	if i > 1 && i < len(runes) {
		i-- // keep last uppercase as start of next word
	}
	for j := 0; j < i; j++ {
		runes[j] = unicode.ToLower(runes[j])
	}
	result := string(runes)
	if GoKeywords[result] {
		result = result + "Val"
	}
	return result
}

func CleanComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Ensure it starts lowercase for godoc style after method name
	return s
}

func RefName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func SchemaTypeStrings(s SchemaObj) []string {
	if s.Type == nil {
		return nil
	}
	// Try as string
	var single string
	if err := json.Unmarshal(s.Type, &single); err == nil {
		return []string{single}
	}
	// Try as array
	var arr []string
	if err := json.Unmarshal(s.Type, &arr); err == nil {
		return arr
	}
	return nil
}

func ToSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func SortedKeys(m map[string]SchemaObj) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func SortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func IsPtrCandidate(goType string) bool {
	switch goType {
	case "string", "int64", "float64", "bool":
		return true
	case "any":
		return false
	}
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") {
		return false
	}
	return true // struct types
}

func PtrWrap(goType string) string {
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") || goType == "any" {
		return goType
	}
	return "*" + goType
}

var PathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func BuildPathExpr(path string, params []PathParam) string {
	if len(params) == 0 {
		return fmt.Sprintf("%q", path)
	}

	paramMap := make(map[string]PathParam)
	for _, p := range params {
		paramMap[p.Name] = p
	}

	// Build fmt.Sprintf expression
	fmtStr := PathParamRe.ReplaceAllStringFunc(path, func(match string) string {
		paramName := match[1 : len(match)-1]
		p, ok := paramMap[paramName]
		if !ok {
			return match
		}
		switch p.GoType {
		case "int64":
			return "%d"
		case "any":
			return "%v"
		default:
			return "%s"
		}
	})

	var args []string
	matches := PathParamRe.FindAllString(path, -1)
	for _, match := range matches {
		paramName := match[1 : len(match)-1]
		p, ok := paramMap[paramName]
		if !ok {
			continue
		}
		args = append(args, CamelCase(p.GoName))
	}

	return fmt.Sprintf("fmt.Sprintf(%q, %s)", fmtStr, strings.Join(args, ", "))
}

func WriteStructDef(b *strings.Builder, sd *StructDef) {
	fmt.Fprintf(b, "// %s represents a component schema.\n", sd.Name)
	fmt.Fprintf(b, "type %s struct {\n", sd.Name)
	for _, f := range sd.Fields {
		typeName := f.Type.Name
		if f.Type.IsPtr {
			typeName = PtrWrap(typeName)
		}
		fmt.Fprintf(b, "\t%s %s %s\n", f.Name, typeName, f.Tag)
	}
	b.WriteString("}\n")
}

func WriteFormattedFile(path, content string) error {
	formatted, err := format.Source([]byte(content))
	if err != nil {
		// Write unformatted for debugging
		os.WriteFile(path+".unformatted", []byte(content), 0644)
		return fmt.Errorf("format %s: %w", path, err)
	}
	return os.WriteFile(path, formatted, 0644)
}
