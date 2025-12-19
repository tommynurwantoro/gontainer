// Package inject provides a reflection-based dependency injection system.
// It automatically creates and wires together objects based on struct tags,
// supporting singletons, private instances, and named dependencies.
//
// See the main package README for detailed usage examples and documentation.
package inject

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
)

// Logger allows for simple logging as inject traverses and populates the
// object graph.
type Logger interface {
	Debugf(format string, v ...interface{})
}

// Populate is a short-hand for populating a graph with the given incomplete
// object values.
func Populate(values ...interface{}) error {
	var g Graph
	for _, v := range values {
		if err := g.Provide(&Object{Value: v}); err != nil {
			return err
		}
	}
	return g.Populate()
}

// An Object in the Graph.
type Object struct {
	Value        interface{}
	Name         string             // Optional
	Complete     bool               // If true, the Value will be considered complete
	Fields       map[string]*Object // Populated with the field names that were injected and their corresponding *Object.
	reflectType  reflect.Type
	reflectValue reflect.Value
	private      bool // If true, the Value will not be used and will only be populated
	created      bool // If true, the Object was created by us
	embedded     bool // If true, the Object is an embedded struct provided internally
}

// String representation suitable for human consumption.
func (o *Object) String() string {
	var buf bytes.Buffer
	fmt.Fprint(&buf, o.reflectType)
	if o.Name != "" {
		fmt.Fprintf(&buf, " named %s", o.Name)
	}
	return buf.String()
}

func (o *Object) addDep(field string, dep *Object) {
	if o.Fields == nil {
		o.Fields = make(map[string]*Object)
	}
	o.Fields[field] = dep
}

// The Graph of Objects.
type Graph struct {
	Logger      Logger // Optional, will trigger debug logging.
	unnamed     []*Object
	unnamedType map[reflect.Type]bool
	named       map[string]*Object
	// Performance optimizations: type index for O(1) lookups
	typeIndex map[reflect.Type][]*Object // Maps types to objects that can be assigned to that type
	// Cache for parsed tags to avoid repeated parsing
	tagCache map[reflect.StructTag]*tag
}

// Provide objects to the Graph. The Object documentation describes
// the impact of various fields.
func (g *Graph) Provide(objects ...*Object) error {
	for _, o := range objects {
		o.reflectType = reflect.TypeOf(o.Value)
		o.reflectValue = reflect.ValueOf(o.Value)

		if o.Fields != nil {
			return fmt.Errorf(
				"fields were specified on object %s when it was provided",
				o,
			)
		}

		if o.Name == "" {
			if !isStructPtr(o.reflectType) {
				return fmt.Errorf(
					"expected unnamed object value to be a pointer to a struct but got type %s "+
						"with value %v",
					o.reflectType,
					o.Value,
				)
			}

			if !o.private {
				if g.unnamedType == nil {
					g.unnamedType = make(map[reflect.Type]bool)
				}

				if g.unnamedType[o.reflectType] {
					return fmt.Errorf(
						"provided two unnamed instances of type *%s.%s",
						o.reflectType.Elem().PkgPath(), o.reflectType.Elem().Name(),
					)
				}
				g.unnamedType[o.reflectType] = true
			}
			g.unnamed = append(g.unnamed, o)
		} else {
			if g.named == nil {
				g.named = make(map[string]*Object)
			}

			if g.named[o.Name] != nil {
				return fmt.Errorf("provided two instances named %s", o.Name)
			}
			g.named[o.Name] = o
		}

		if g.Logger != nil {
			if o.created {
				g.Logger.Debugf("created %s", o)
			} else if o.embedded {
				g.Logger.Debugf("provided embedded %s", o)
			} else {
				g.Logger.Debugf("provided %s", o)
			}
		}
	}
	return nil
}

// Populate the incomplete Objects.
func (g *Graph) Populate() error {
	for _, o := range g.named {
		if o.Complete {
			continue
		}

		if err := g.populateExplicit(o); err != nil {
			return err
		}
	}

	// We append and modify our slice as we go along, so we don't use a standard
	// range loop, and do a single pass thru each object in our graph.
	i := 0
	for {
		if i == len(g.unnamed) {
			break
		}

		o := g.unnamed[i]
		i++

		if o.Complete {
			continue
		}

		if err := g.populateExplicit(o); err != nil {
			return err
		}
	}

	// A Second pass handles injecting Interface values to ensure we have created
	// all concrete types first.
	for _, o := range g.unnamed {
		if o.Complete {
			continue
		}

		if err := g.populateUnnamedInterface(o); err != nil {
			return err
		}
	}

	for _, o := range g.named {
		if o.Complete {
			continue
		}

		if err := g.populateUnnamedInterface(o); err != nil {
			return err
		}
	}

	return nil
}

func (g *Graph) populateExplicit(o *Object) error {
	// Ignore named value types.
	if o.Name != "" && !isStructPtr(o.reflectType) {
		return nil
	}

StructLoop:
	for i := 0; i < o.reflectValue.Elem().NumField(); i++ {
		field := o.reflectValue.Elem().Field(i)
		fieldType := field.Type()
		fieldTag := o.reflectType.Elem().Field(i).Tag
		fieldName := o.reflectType.Elem().Field(i).Name
		tag, err := g.parseTagCached(fieldTag)
		if err != nil {
			// Check if it's a malformed tag error and format accordingly
			if strings.Contains(err.Error(), "malformed inject tag") {
				return fmt.Errorf(
					"unexpected tag format `%s` for field %s in type %s",
					string(fieldTag),
					o.reflectType.Elem().Field(i).Name,
					o.reflectType,
				)
			}
			return fmt.Errorf(
				"unexpected tag format `%s` for field %s in type %s: %w",
				string(fieldTag),
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
				err,
			)
		}

		// Skip fields without a tag.
		if tag == nil {
			continue
		}

		// Cannot be used with unexported fields.
		if !field.CanSet() {
			return fmt.Errorf(
				"inject requested on unexported field %s in type %s",
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
			)
		}

		// Inline tag on anything besides a struct is considered invalid.
		if tag.Inline && fieldType.Kind() != reflect.Struct {
			return fmt.Errorf(
				"inline requested on non inlined field %s in type %s",
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
			)
		}

		// Don't overwrite existing values.
		if !isNilOrZero(field, fieldType) {
			continue
		}

		// Named injects must have been explicitly provided.
		if tag.Name != "" {
			existing := g.named[tag.Name]
			if existing == nil {
				return fmt.Errorf(
					"did not find object named %s required by field %s in type %s",
					tag.Name,
					o.reflectType.Elem().Field(i).Name,
					o.reflectType,
				)
			}

			if !existing.reflectType.AssignableTo(fieldType) {
				return fmt.Errorf(
					"object named %s of type %s is not assignable to field %s (%s) in type %s",
					tag.Name,
					fieldType,
					o.reflectType.Elem().Field(i).Name,
					existing.reflectType,
					o.reflectType,
				)
			}

			field.Set(reflect.ValueOf(existing.Value))
			if g.Logger != nil {
				g.Logger.Debugf(
					"assigned %s to field %s in %s",
					existing,
					o.reflectType.Elem().Field(i).Name,
					o,
				)
			}
			o.addDep(fieldName, existing)
			continue StructLoop
		}

		// Inline struct values indicate we want to traverse into it, but not
		// inject itself. We require an explicit "inline" tag for this to work.
		if fieldType.Kind() == reflect.Struct {
			if tag.Private {
				return fmt.Errorf(
					"cannot use private inject on inline struct on field %s in type %s",
					o.reflectType.Elem().Field(i).Name,
					o.reflectType,
				)
			}

			if !tag.Inline {
				return fmt.Errorf(
					"inline struct on field %s in type %s requires an explicit \"inline\" tag",
					o.reflectType.Elem().Field(i).Name,
					o.reflectType,
				)
			}

			err := g.Provide(&Object{
				Value:    field.Addr().Interface(),
				private:  true,
				embedded: o.reflectType.Elem().Field(i).Anonymous,
			})
			if err != nil {
				return err
			}
			continue
		}

		// Interface injection is handled in a second pass.
		if fieldType.Kind() == reflect.Interface {
			continue
		}

		// Maps are created and required to be private.
		if fieldType.Kind() == reflect.Map {
			if !tag.Private {
				return fmt.Errorf(
					"inject on map field %s in type %s must be named or private",
					o.reflectType.Elem().Field(i).Name,
					o.reflectType,
				)
			}

			field.Set(reflect.MakeMap(fieldType))
			if g.Logger != nil {
				g.Logger.Debugf(
					"made map for field %s in %s",
					o.reflectType.Elem().Field(i).Name,
					o,
				)
			}
			continue
		}

		// Can only inject Pointers from here on.
		if !isStructPtr(fieldType) {
			return fmt.Errorf(
				"found inject tag on unsupported field %s in type %s",
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
			)
		}

		// Unless it's a private inject, we'll look for an existing instance of the
		// same type using optimized type index.
		if !tag.Private {
			// Build type index if not already built
			if g.typeIndex == nil {
				g.buildTypeIndex()
			}

			// Try direct type match first (fastest path)
			if candidates := g.typeIndex[fieldType]; len(candidates) > 0 {
				for _, existing := range candidates {
					if existing.private {
						continue
					}
					field.Set(reflect.ValueOf(existing.Value))
					if g.Logger != nil {
						g.Logger.Debugf(
							"assigned existing %s to field %s in %s",
							existing,
							o.reflectType.Elem().Field(i).Name,
							o,
						)
					}
					o.addDep(fieldName, existing)
					continue StructLoop
				}
			}

			// Fallback to checking all objects if direct match failed (for interface types)
			for _, existing := range g.unnamed {
				if existing.private {
					continue
				}
				if existing.reflectType.AssignableTo(fieldType) {
					field.Set(reflect.ValueOf(existing.Value))
					if g.Logger != nil {
						g.Logger.Debugf(
							"assigned existing %s to field %s in %s",
							existing,
							o.reflectType.Elem().Field(i).Name,
							o,
						)
					}
					o.addDep(fieldName, existing)
					continue StructLoop
				}
			}
		}

		newValue := reflect.New(fieldType.Elem())
		newObject := &Object{
			Value:   newValue.Interface(),
			private: tag.Private,
			created: true,
		}

		// Add the newly ceated object to the known set of objects.
		err = g.Provide(newObject)
		if err != nil {
			return err
		}

		// Finally assign the newly created object to our field.
		field.Set(newValue)
		if g.Logger != nil {
			g.Logger.Debugf(
				"assigned newly created %s to field %s in %s",
				newObject,
				o.reflectType.Elem().Field(i).Name,
				o,
			)
		}
		o.addDep(fieldName, newObject)
	}
	return nil
}

func (g *Graph) populateUnnamedInterface(o *Object) error {
	// Ignore named value types.
	if o.Name != "" && !isStructPtr(o.reflectType) {
		return nil
	}

	for i := 0; i < o.reflectValue.Elem().NumField(); i++ {
		field := o.reflectValue.Elem().Field(i)
		fieldType := field.Type()
		fieldTag := o.reflectType.Elem().Field(i).Tag
		fieldName := o.reflectType.Elem().Field(i).Name
		tag, err := g.parseTagCached(fieldTag)
		if err != nil {
			// Check if it's a malformed tag error and format accordingly
			if strings.Contains(err.Error(), "malformed inject tag") {
				return fmt.Errorf(
					"unexpected tag format `%s` for field %s in type %s",
					string(fieldTag),
					o.reflectType.Elem().Field(i).Name,
					o.reflectType,
				)
			}
			return fmt.Errorf(
				"unexpected tag format `%s` for field %s in type %s: %w",
				string(fieldTag),
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
				err,
			)
		}

		// Skip fields without a tag.
		if tag == nil {
			continue
		}

		// We only handle interface injection here. Other cases including errors
		// are handled in the first pass when we inject pointers.
		if fieldType.Kind() != reflect.Interface {
			continue
		}

		// Interface injection can't be private because we can't instantiate new
		// instances of an interface.
		if tag.Private {
			return fmt.Errorf(
				"found private inject tag on interface field %s in type %s",
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
			)
		}

		// Don't overwrite existing values.
		if !isNilOrZero(field, fieldType) {
			continue
		}

		// Named injects must have already been handled in populateExplicit.
		if tag.Name != "" {
			panic(fmt.Sprintf("unhandled named instance with name %s", tag.Name))
		}

		// Find one, and only one assignable value for the field.
		// For interfaces, we need to check all objects since type index only has concrete types.
		var found *Object
		for _, existing := range g.unnamed {
			if existing.private {
				continue
			}
			if existing.reflectType.AssignableTo(fieldType) {
				if found != nil {
					return fmt.Errorf(
						"found two assignable values for field %s in type %s. one type "+
							"%s with value %v and another type %s with value %v",
						o.reflectType.Elem().Field(i).Name,
						o.reflectType,
						found.reflectType,
						found.Value,
						existing.reflectType,
						existing.reflectValue,
					)
				}
				found = existing
				field.Set(reflect.ValueOf(existing.Value))
				if g.Logger != nil {
					g.Logger.Debugf(
						"assigned existing %s to interface field %s in %s",
						existing,
						o.reflectType.Elem().Field(i).Name,
						o,
					)
				}
				o.addDep(fieldName, existing)
			}
		}

		// If we didn't find an assignable value, we're missing something.
		if found == nil {
			return fmt.Errorf(
				"found no assignable value for field %s in type %s",
				o.reflectType.Elem().Field(i).Name,
				o.reflectType,
			)
		}
	}
	return nil
}

// Objects returns all known objects, named as well as unnamed. The returned
// elements are not in a stable order.
func (g *Graph) Objects() []*Object {
	objects := make([]*Object, 0, len(g.unnamed)+len(g.named))
	for _, o := range g.unnamed {
		if !o.embedded {
			objects = append(objects, o)
		}
	}
	for _, o := range g.named {
		if !o.embedded {
			objects = append(objects, o)
		}
	}
	return objects
}

var (
	injectOnly    = &tag{}
	injectPrivate = &tag{Private: true}
	injectInline  = &tag{Inline: true}
)

type tag struct {
	Name    string
	Inline  bool
	Private bool
}

// parseTag parses the inject tag from a struct tag string.
// It replaces the old structtag.Extract with standard library reflect.StructTag.
// Uses caching to avoid repeated parsing of the same tags.
func (g *Graph) parseTagCached(tagStr reflect.StructTag) (*tag, error) {
	// Check cache first
	if g.tagCache == nil {
		g.tagCache = make(map[reflect.StructTag]*tag)
	}
	if cached, ok := g.tagCache[tagStr]; ok {
		return cached, nil
	}

	// Validate tag format before parsing
	// Check for malformed tags like `inject:` (colon with no value) or `inject:"` (unclosed quote)
	tagString := string(tagStr)
	if strings.Contains(tagString, "inject:") {
		// Check for malformed patterns:
		// 1. Tag ends with just "inject:" (no value after colon)
		// 2. Tag contains "inject:\"" but doesn't have a closing quote (not "inject:\"\"" or "inject:\"value\"")
		if strings.HasSuffix(tagString, "inject:") {
			// This is a malformed tag - return error
			return nil, fmt.Errorf("malformed inject tag: %s", tagString)
		}
		// Check for unclosed quote: "inject:\"" without proper closing
		if strings.Contains(tagString, "inject:\"") {
			// Count quotes after "inject:\""
			idx := strings.Index(tagString, "inject:\"")
			remaining := tagString[idx+8:] // After "inject:\""
			// If remaining doesn't contain a closing quote or is empty, it's malformed
			if remaining == "" || (!strings.Contains(remaining, "\"") && !strings.Contains(remaining, " ")) {
				return nil, fmt.Errorf("malformed inject tag: %s", tagString)
			}
		}
	}

	// Parse tag
	value, ok := tagStr.Lookup("inject")
	if !ok {
		g.tagCache[tagStr] = nil
		return nil, nil
	}

	var result *tag
	switch value {
	case "":
		result = injectOnly
	case "inline":
		result = injectInline
	case "private":
		result = injectPrivate
	default:
		// Named dependency - value is the name
		// Handle comma-separated values (e.g., "name,option")
		parts := strings.Split(value, ",")
		name := strings.TrimSpace(parts[0])
		result = &tag{Name: name}
	}

	g.tagCache[tagStr] = result
	return result, nil
}

// buildTypeIndex builds an index mapping types to objects that can be assigned to those types.
// This enables faster lookups by pre-indexing objects by their concrete types.
// Note: For interface types, we still need to check assignability during lookup,
// but this reduces the search space significantly.
func (g *Graph) buildTypeIndex() {
	if g.typeIndex != nil {
		return
	}
	g.typeIndex = make(map[reflect.Type][]*Object)

	// Index all unnamed objects by their concrete types
	for _, obj := range g.unnamed {
		if obj.private {
			continue
		}
		objType := obj.reflectType

		// Add direct type match - objects can be assigned to their own type
		g.typeIndex[objType] = append(g.typeIndex[objType], obj)

		// For interface lookups, we'll check all objects during lookup
		// but having them indexed by concrete type helps narrow the search
	}
}

func isStructPtr(t reflect.Type) bool {
	return t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct
}

// isNilOrZero checks if a value is nil or zero more efficiently than reflect.DeepEqual.
func isNilOrZero(v reflect.Value, t reflect.Type) bool {
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return v.IsNil()
	case reflect.String:
		return v.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !isNilOrZero(v.Index(i), v.Index(i).Type()) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !isNilOrZero(v.Field(i), v.Field(i).Type()) {
				return false
			}
		}
		return true
	default:
		// Fallback to DeepEqual for complex types, but this should be rare
		return reflect.DeepEqual(v.Interface(), reflect.Zero(t).Interface())
	}
}
