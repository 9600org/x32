package x32

import (
	"fmt"
	"net"
	"reflect"

	"github.com/9600org/go-osc/osc"
)

type Client interface {
	Send(osc.Packet) error
}

type UDPClient struct {
	Conn *net.UDPConn
}

var _ Client = &UDPClient{}

func (c *UDPClient) Send(p osc.Packet) error {
	data, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	if _, err := c.Conn.Write(data); err != nil {
		return err
	}
	return nil
}

func FloatToInt(a interface{}) (int32, error) {
	f, ok := a.(float32)
	if !ok {
		return 0, fmt.Errorf("failed to convert argument type %T to float32", a)
	}
	return int32(f), nil
}

func IntToFloat(a interface{}) (float32, error) {
	i, ok := a.(int32)
	if !ok {
		return 0, fmt.Errorf("failed to convert argument type %T to int32", a)
	}
	return float32(i), nil
}

func NotInt(a interface{}) (int32, error) {
	v, ok := a.(int32)
	if !ok {
		return 0, fmt.Errorf("failed to convert argument type %T to int32", a)
	}
	if v == 0 {
		v = 1
	} else {
		v = 0
	}
	return v, nil
}

func isArgEq(m osc.Message, idx int, v interface{}) (bool, error) {
	if l := len(m.Arguments); l <= idx {
		return false, fmt.Errorf("message has %d arguments, can't get argument %d", l, idx)
	}
	if aT, bT := reflect.TypeOf(m.Arguments[idx]), reflect.TypeOf(v); aT != bT {
		return false, fmt.Errorf("argument type %s != value type %s", aT, bT)
	}
	return reflect.DeepEqual(m.Arguments[idx], v), nil
}

func getIntArg(m osc.Message, i int) (int32, error) {
	if l := len(m.Arguments); l <= i {
		return 0, fmt.Errorf("insufficient args (%d), wanted > %d", l, i)
	}
	r, ok := m.Arguments[i].(int32)
	if !ok {
		return 0, fmt.Errorf("got arg type %T, wanted int32)", m.Arguments[i])
	}
	return r, nil
}

func getFloatArg(m osc.Message, i int) (float32, error) {
	if l := len(m.Arguments); l <= i {
		return 0, fmt.Errorf("insufficient args (%d), wanted > %d", l, i)
	}
	r, ok := m.Arguments[i].(float32)
	if !ok {
		return 0, fmt.Errorf("got arg type %T, wanted float32)", m.Arguments[i])
	}
	return r, nil
}
