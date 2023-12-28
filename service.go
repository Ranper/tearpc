package tearpc

import "reflect"

type Service struct {
	Methods map[string]*reflect.Value
}
