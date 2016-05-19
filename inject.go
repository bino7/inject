// Package inject provides utilities for mapping and injecting dependencies in various ways.
package inject

import (
	"fmt"
	"reflect"
	"github.com/bino7/inject"
)

/*type Injectors interface {
	All() []Injector
}*/

// Injector represents an interface for mapping and injecting dependencies into structs
// and function arguments.
type Injector interface {
	/*Injectors*/

	Applicator
	Invoker
	TypeMapper
	// SetParent sets the parent of the injector. If the injector cannot find a
	// dependency in its Type map it will check its parent before returning an
	// error.
	SetParent(Injector)
	Start()
	Stop()
	Events() chan inject.Event
	On(key string, handlers ...Handler)
	Fire(key string, data interface{})
}

// Applicator represents an interface for mapping dependencies to a struct.
type Applicator interface {
	// Maps dependencies in the Type map to each field in the struct
	// that is tagged with 'inject'. Returns an error if the injection
	// fails.
	Apply(interface{}) error
}

// Invoker represents an interface for calling functions via reflection.
type Invoker interface {
	// Invoke attempts to call the interface{} provided as a function,
	// providing dependencies for function arguments based on Type. Returns
	// a slice of reflect.Value representing the returned values of the function.
	// Returns an error if the injection fails.
	Invoke(interface{}) ([]reflect.Value, error)
}

// TypeMapper represents an interface for mapping interface{} values based on type.
type TypeMapper interface {
	// Maps the interface{} value based on its immediate type from reflect.TypeOf.
	Map(interface{}) TypeMapper
	// Maps the interface{} value based on the pointer of an Interface provided.
	// This is really only useful for mapping a value as an interface, as interfaces
	// cannot at this time be referenced directly without a pointer.
	MapTo(interface{}, interface{}) TypeMapper
	// Provides a possibility to directly insert a mapping based on type and value.
	// This makes it possible to directly map type arguments not possible to instantiate
	// with reflect like unidirectional channels.
	Set(reflect.Type, reflect.Value) TypeMapper
	// Returns the Value that is mapped to the current type. Returns a zeroed Value if
	// the Type has not been mapped.
	Get(reflect.Type) reflect.Value
}

type Event struct {
	Src  Injector
	Type string
	Data interface{}
}

type Handler interface{}

func validateHandler(handler Handler) {
	t := reflect.TypeOf(handler)
	if t.Kind() != reflect.Func {
		panic("inject handler must be a callable func")
	}
	if t.NumIn() == 0 && t.In(0) != Event.Type {
		panic("the first arg of inject handler must be a Event type")
	}
}

type injector struct {
	values   map[reflect.Type]reflect.Value
	handlers map[string][]Handler
	events   chan Event
	stopped  chan bool
	parent   Injector
	/*injectors     []*injector
	injectorsLock sync.RWMutex*/
}

// InterfaceOf dereferences a pointer to an Interface type.
// It panics if value is not an pointer to an interface.
func InterfaceOf(value interface{}) reflect.Type {
	t := reflect.TypeOf(value)

	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Interface {
		panic("Called inject.InterfaceOf with a value that is not a pointer to an interface. (*MyInterface)(nil)")
	}

	return t
}

// New returns a new Injector.
func New() Injector {
	return &injector{
		values: make(map[reflect.Type]reflect.Value),
		handlers: make(map[string][]Handler),
		events: make(chan Event),
		stopped: make(chan bool),
		/*injectors: make([]*injector,0),*/
	}
}

// Invoke attempts to call the interface{} provided as a function,
// providing dependencies for function arguments based on Type.
// Returns a slice of reflect.Value representing the returned values of the function.
// Returns an error if the injection fails.
// It panics if f is not a function
func (inj *injector) Invoke(f interface{}) ([]reflect.Value, error) {
	t := reflect.TypeOf(f)

	var in = make([]reflect.Value, t.NumIn()) //Panic if t is not kind of Func
	for i := 0; i < t.NumIn(); i++ {
		argType := t.In(i)
		val := inj.Get(argType)
		if !val.IsValid() {
			return nil, fmt.Errorf("Value not found for type %v", argType)
		}

		in[i] = val
	}

	return reflect.ValueOf(f).Call(in), nil
}

// Maps dependencies in the Type map to each field in the struct
// that is tagged with 'inject'.
// Returns an error if the injection fails.
func (inj *injector) Apply(val interface{}) error {
	v := reflect.ValueOf(val)

	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil // Should not panic here ?
	}

	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		structField := t.Field(i)
		if f.CanSet() && (structField.Tag == "inject" || structField.Tag.Get("inject") != "") {
			ft := f.Type()
			v := inj.Get(ft)
			if !v.IsValid() {
				return fmt.Errorf("Value not found for type %v", ft)
			}

			f.Set(v)
		}

	}

	return nil
}

// Maps the concrete value of val to its dynamic type using reflect.TypeOf,
// It returns the TypeMapper registered in.
func (i *injector) Map(val interface{}) TypeMapper {
	i.values[reflect.TypeOf(val)] = reflect.ValueOf(val)
	return i
}

func (i *injector) MapTo(val interface{}, ifacePtr interface{}) TypeMapper {
	i.values[InterfaceOf(ifacePtr)] = reflect.ValueOf(val)
	return i
}

// Maps the given reflect.Type to the given reflect.Value and returns
// the Typemapper the mapping has been registered in.
func (i *injector) Set(typ reflect.Type, val reflect.Value) TypeMapper {
	i.values[typ] = val
	return i
}

func (i *injector) Get(t reflect.Type) reflect.Value {
	val := i.values[t]

	if val.IsValid() {
		return val
	}

	// no concrete types found, try to find implementors
	// if t is an interface
	if t.Kind() == reflect.Interface {
		for k, v := range i.values {
			if k.Implements(t) {
				val = v
				break
			}
		}
	}

	// Still no type found, try to look it up on the parent
	if !val.IsValid() && i.parent != nil {
		val = i.parent.Get(t)
	}

	return val

}

func (i *injector) SetParent(parent Injector) {
	i.parent = parent
}

func (i *injector)On(key string, handlers ...Handler) {
	for _, h := range handlers {
		validateHandler(h)
	}
	if i.handlers[key] == nil {
		i.handlers[key] = handlers
	} else {
		i.handlers[key] = append(i.handlers[key], handlers...)
	}
	return i
}
func (i *injector)Fire(key string, data interface{}) {
	if i.handlers[key] != nil {
		e := Event{
			Src:i,
			Type:key,
			Data:data,
		}
		i.events <- e
	}
}

func (i *injector)run(e Event) {
	hs := i.handlers[e.Type]
	if hs == nil {
		if i.parent == nil {
			panic(fmt.Sprintf("%s %s", "unknow event type ", e.Type))
		}
		i.parent.Events <- e
	} else {
		i.Set(Event.Type, e)
		for _, h := range hs {
			i.Invoke(h)
		}
	}
}

func (i *injector)Start() {
	go func() {
		for {
			select {
			case e := <-i.events:
				i.run(e)
			case <-i.stopped:
				return
			}
		}
	}()
}

func (i *injector)Stop() {
	i.stopped <- true
}

/*func (i *injector)All() {
	i.injectorsLock.RLock()
	defer i.injectorsLock.RUnlock()
	return i.injectors
}*/

func (i *injector)Events() chan <- Event {
	return i.events
}

