package tearpc

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

// 封装一个服务类的方法 "Service.Method" 中的method
type methodType struct {
	method    reflect.Method // 方法本身
	ArgType   reflect.Type   // 第一个参数类型
	ReplyType reflect.Type   // 第二个参数类型
	numCalls  uint64         // 接口被调用的次数
}

// 因为包含非原始类型,这里使用指针

// 获取被调用的次数
func (m *methodType) NumberCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls)
}

// Elem(): 必须是指针类型,返回Array, Chan, Map, Pointer, or Slice中元素的类型

// new一个当前函数的输入参数变量
func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value

	if m.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(m.ArgType.Elem())
	} else {
		argv = reflect.New(m.ArgType).Elem()
	}
	return argv
}

// 创建reply参数的实例
func (m *methodType) newReplyv() reflect.Value {
	// reply must be a pointer type
	replyv := reflect.New(m.ReplyType.Elem()) // 获取指针指向的元素类型,并创建一个零值
	// 指针和值类型创建实例的方式有点不同
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem())) // 第三定律
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}

	log.Println("newReplyv: replyv", replyv)
	return replyv
}

// 定义service
type service struct {
	name   string                 // 提供服务的结构体的名字,比如MathService
	typ    reflect.Type           // 结构体的类型定义,提供服务的结构体
	rcvr   reflect.Value          // receiver 实例本身, 通常作为方法的第一个参数
	method map[string]*methodType // 函数名,映射到具体的接口: "add" -> add(param1, *param2) 存储映射的结构体的所有符合条件的方法
}

// 定义构造函数
func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)
	s.name = reflect.Indirect(s.rcvr).Type().Name()
	s.typ = reflect.TypeOf(rcvr)
	// 判断当前服务类是不是导出的
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}
	s.registerMetods()
	return s
}

func isExportedOrBuiltinType(t reflect.Type) bool {
	return ast.IsExported(t.Name()) || t.PkgPath() == "" //导出或者内置类型
}

func (s *service) registerMetods() {

	s.method = make(map[string]*methodType)
	// 获取该服务类所有导出的方法
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i)
		mType := method.Type //?方法也有type
		// 过滤掉不符合要求的接口. 输入参数必须为3️(其中第一个是接受者, 第二个是请求,第三个是指向响应的指针), 返回类型是一个:error
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		// 把nil转换为error指针类型,然后再利用TypeOf获取其类型(指针), 再通过Elem获取类型(error)
		// 返回值必须是error类型
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() { //? 这里难道不能直接指定是error类型吗?
			continue
		}

		argType, replyType := mType.In(1), mType.In(2)
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
			numCalls:  0, //
		}
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
}

func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func
	// 这里的参数输入是[]reflect.Value的形式 用argv 和replyv做初始参数; 返回参数也是个[]reflect.Value
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv})
	if errInter := returnValues[0].Interface(); errInter != nil { // 如果正常发生,返回的应该是nil,否则将其转换为error类型
		return errInter.(error) // 接口断言
	}
	return nil
}
