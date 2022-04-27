package znet

import (
	"github.com/sohaha/zlsgo/zdi"
)

func handlerFuncs(h []Handler) []handlerFn {
	more := make([]handlerFn, len(h))
	for i := range h {
		more[i] = handlerFunc(h[i])
	}
	return more
}

func handlerFunc(h Handler) (fn handlerFn) {
	switch v := h.(type) {
	case func(*Context):
		return func(c *Context) error {
			v(c)
			return nil
		}
	case handlerFn:
		return v
	case zdi.PreInvoker:
		return func(c *Context) error {
			_, err := c.Injector.Invoke(v)
			return err
		}
	case func() (int, string):
		return func(c *Context) error {
			v, err := c.Injector.Invoke(invokerCodeText(v))
			c.String(int32(v[0].Int()), v[1].String())
			return err
		}
	default:
		return func(c *Context) error {
			v, err := c.Injector.Invoke(v)
			if err != nil {
				return err
			}
			for i := range v {
				value := v[i]
				v := value.Interface()
				switch vv := v.(type) {
				case int:
					c.prevData.Code.Store(int32(vv))
				case int32:
					c.prevData.Code.Store(vv)
				case uint:
					c.prevData.Code.Store(int32(vv))
				case string:
					c.render = &renderString{Format: vv}
				case error:
					err = vv
				case []byte:
					c.render = &renderByte{Data: vv}
				case ApiData:
					c.render = &renderJSON{Data: vv}
				}
			}
			return err
		}
	}
}
