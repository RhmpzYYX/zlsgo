package ztype

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/sohaha/zlsgo/zreflect"
)

type Conver struct {
	MatchName  func(mapKey, fieldName string) bool
	TagName    string
	ZeroFields bool
	Squash     bool
	Deep       bool
	Merge      bool
	ConvHook   func(i reflect.Value, o reflect.Type) (reflect.Value, error)
}

var conv = Conver{TagName: tagName, Squash: true, MatchName: strings.EqualFold}

func To(input, out interface{}, opt ...func(*Conver)) error {
	return ValueConv(input, zreflect.ValueOf(out), opt...)
}

func ValueConv(input interface{}, out reflect.Value, opt ...func(*Conver)) error {
	o := conv
	for _, f := range opt {
		f(&o)
	}
	if out.Kind() != reflect.Ptr {
		return errors.New("out must be a pointer")
	}
	if !out.Elem().CanAddr() {
		return errors.New("out must be addressable (a pointer)")
	}
	return o.to("input", input, out)
}

func (d *Conver) to(name string, input interface{}, outVal reflect.Value) error {
	var inputVal reflect.Value
	if input != nil {
		inputVal = zreflect.ValueOf(input)
		if inputVal.Kind() == reflect.Ptr && inputVal.IsNil() {
			input = nil
		}
	}

	t := outVal.Type()
	if input == nil {
		if d.ZeroFields {
			outVal.Set(reflect.Zero(t))
		}
		return nil
	}

	if !inputVal.IsValid() {
		outVal.Set(reflect.Zero(t))
		return nil
	}

	var err error
	outputKind := zreflect.GetAbbrKind(outVal)

	if d.ConvHook != nil {
		if i, err := d.ConvHook(inputVal, t); err != nil {
			return err
		} else {
			input = i.Interface()
		}
	}

	switch outputKind {
	case reflect.Bool:
		outVal.SetBool(ToBool(input))
	case reflect.Interface:
		err = d.basic(name, input, outVal)
	case reflect.String:
		outVal.SetString(ToString(input))
	case reflect.Int:
		outVal.SetInt(ToInt64(input))
	case reflect.Uint:
		outVal.SetUint(ToUint64(input))
	case reflect.Float64:
		outVal.SetFloat(ToFloat64(input))
	case reflect.Struct:
		err = d.toStruct(name, input, outVal)
	case reflect.Map:
		err = d.toMap(name, input, outVal)
	case reflect.Ptr:
		err = d.toPtr(name, input, outVal)
	case reflect.Slice:
		err = d.toSlice(name, input, outVal)
	case reflect.Array:
		err = d.toArray(name, input, outVal)
	case reflect.Func:
		err = d.toFunc(name, input, outVal)
	default:
		return errors.New(name + ": unsupported type: " + outputKind.String())
	}

	return err
}

func (d *Conver) basic(name string, data interface{}, val reflect.Value) error {
	if val.IsValid() && val.Elem().IsValid() {
		elem, copied := val.Elem(), false
		if !elem.CanAddr() {
			copied = true
			nVal := reflect.New(elem.Type())
			nVal.Elem().Set(elem)
			elem = nVal
		}

		if err := d.to(name, data, elem); err != nil || !copied {
			return err
		}

		val.Set(elem.Elem())
		return nil
	}

	dataVal := zreflect.ValueOf(data)
	if dataVal.Kind() == reflect.Ptr && dataVal.Type().Elem() == val.Type() {
		dataVal = reflect.Indirect(dataVal)
	}

	if !dataVal.IsValid() {
		dataVal = reflect.Zero(val.Type())
	}

	dataValType := dataVal.Type()
	if !dataValType.AssignableTo(val.Type()) {
		return fmt.Errorf(
			"'%s' expected type '%s', got '%s'",
			name, val.Type(), dataValType)
	}

	val.Set(dataVal)
	return nil
}

func (d *Conver) toStruct(name string, data interface{}, val reflect.Value) error {
	dataVal := reflect.Indirect(zreflect.ValueOf(data))
	if dataVal.Type() == val.Type() {
		val.Set(dataVal)
		return nil
	}

	switch dataVal.Kind() {
	case reflect.Map:
		return d.toStructFromMap(name, dataVal, val)
	case reflect.Struct:
		mapType := reflect.TypeOf((map[string]interface{})(nil))
		mval := reflect.MakeMap(mapType)
		addrVal := reflect.New(mval.Type())
		reflect.Indirect(addrVal).Set(mval)
		err := d.toMapFromStruct(name, dataVal, reflect.Indirect(addrVal), mval)
		if err != nil {
			return err
		}

		return d.toStructFromMap(name, reflect.Indirect(addrVal), val)

	default:
		return fmt.Errorf("'%s' expected a map, got '%s'", name, dataVal.Kind())
	}
}

func (d *Conver) toStructFromMap(name string, dataVal, val reflect.Value) error {
	dataValType := dataVal.Type()
	if kind := dataValType.Key().Kind(); kind != reflect.String && kind != reflect.Interface {
		return fmt.Errorf(
			"'%s' needs a map with string keys, has '%s' keys",
			name, dataValType.Key().Kind())
	}

	dataValKeys := make(map[reflect.Value]struct{})
	dataValKeysUnused := make(map[interface{}]struct{})
	for _, dataValKey := range dataVal.MapKeys() {
		dataValKeys[dataValKey] = struct{}{}
		dataValKeysUnused[dataValKey.Interface()] = struct{}{}
	}

	targetValKeysUnused, structs := make(map[interface{}]struct{}), make([]reflect.Value, 1, 5)
	structs[0] = val

	type field struct {
		val   reflect.Value
		field reflect.StructField
	}

	var remainField *field

	fields := make([]field, 0)
	for len(structs) > 0 {
		structVal := structs[0]
		structs = structs[1:]
		structType := structVal.Type()

		for i := 0; i < structType.NumField(); i++ {
			fieldType := structType.Field(i)
			fieldVal := structVal.Field(i)
			if fieldVal.Kind() == reflect.Ptr && fieldVal.Elem().Kind() == reflect.Struct {
				fieldVal = fieldVal.Elem()
			}

			squash := d.Squash && fieldVal.Kind() == reflect.Struct && fieldType.Anonymous
			remain := false
			_, opt := zreflect.GetStructTag(fieldType, d.TagName, tagNameLesser)
			tagParts := strings.Split(opt, ",")
			for _, tag := range tagParts {
				if tag == "squash" {
					squash = true
					break
				}

				if tag == "remain" {
					remain = true
					break
				}
			}

			if squash {
				if fieldVal.Kind() != reflect.Struct {
					return fmt.Errorf("cannot squash non-struct type '%s'", fieldVal.Type())
				} else {
					structs = append(structs, fieldVal)
				}
				continue
			}

			if remain {
				remainField = &field{field: fieldType, val: fieldVal}
			} else {
				fields = append(fields, field{field: fieldType, val: fieldVal})
			}
		}
	}

	for _, f := range fields {
		field, fieldValue := f.field, f.val
		fieldName, _ := zreflect.GetStructTag(field, d.TagName, tagNameLesser)
		rawMapKey := zreflect.ValueOf(fieldName)
		rawMapVal := dataVal.MapIndex(rawMapKey)
		if !rawMapVal.IsValid() {
			for dataValKey := range dataValKeys {
				mK, ok := dataValKey.Interface().(string)
				if !ok {
					continue
				}

				if d.MatchName(mK, fieldName) {
					rawMapKey = dataValKey
					rawMapVal = dataVal.MapIndex(dataValKey)
					break
				}
			}

			if !rawMapVal.IsValid() {
				targetValKeysUnused[fieldName] = struct{}{}
				continue
			}
		}

		if !fieldValue.IsValid() {
			panic("field is not valid")
		}

		if !fieldValue.CanSet() {
			continue
		}

		delete(dataValKeysUnused, rawMapKey.Interface())

		if name != "" {
			fieldName = name + "." + fieldName
		}

		if err := d.to(fieldName, rawMapVal.Interface(), fieldValue); err != nil {
			return err
		}
	}

	if remainField != nil && len(dataValKeysUnused) > 0 {
		remain := map[interface{}]interface{}{}
		for key := range dataValKeysUnused {
			remain[key] = dataVal.MapIndex(zreflect.ValueOf(key)).Interface()
		}

		if err := d.toMap(name, remain, remainField.val); err != nil {
			return err
		}
	}

	return nil
}

func (d *Conver) toMap(name string, data interface{}, val reflect.Value) error {
	valType := val.Type()
	valKeyType := valType.Key()
	valElemType := valType.Elem()
	valMap := val

	if valMap.IsNil() || d.ZeroFields {
		mapType := reflect.MapOf(valKeyType, valElemType)
		valMap = reflect.MakeMap(mapType)
	}

	dataVal := reflect.Indirect(zreflect.ValueOf(data))
	switch dataVal.Kind() {
	case reflect.Map:
		return d.toMapFromMap(name, dataVal, val, valMap)
	case reflect.Struct:
		return d.toMapFromStruct(name, dataVal, val, valMap)
	case reflect.Array, reflect.Slice:
		return d.toMapFromSlice(name, dataVal, val, valMap)
	default:
		return fmt.Errorf("'%s' expected a map, got '%s'", name, dataVal.Kind())
	}
}

func (d *Conver) toMapFromSlice(name string, dataVal reflect.Value, val reflect.Value, valMap reflect.Value) error {
	if dataVal.Len() == 0 {
		val.Set(valMap)
		return nil
	}

	for i := 0; i < dataVal.Len(); i++ {
		err := d.to(
			name+"["+strconv.Itoa(i)+"]",
			dataVal.Index(i).Interface(), val)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Conver) toMapFromMap(name string, dataVal reflect.Value, val reflect.Value, valMap reflect.Value) error {
	valType := val.Type()
	valKeyType := valType.Key()
	valElemType := valType.Elem()

	if dataVal.Len() == 0 {
		if dataVal.IsNil() {
			if !val.IsNil() {
				val.Set(dataVal)
			}
		} else {
			val.Set(valMap)
		}

		return nil
	}

	for _, k := range dataVal.MapKeys() {
		fieldName := name + "[" + k.String() + "]"
		currentKey := reflect.Indirect(reflect.New(valKeyType))
		if err := d.to(fieldName, k.Interface(), currentKey); err != nil {
			return err
		}

		v := dataVal.MapIndex(k).Interface()
		currentVal := reflect.Indirect(reflect.New(valElemType))
		if err := d.to(fieldName, v, currentVal); err != nil {
			return err
		}

		valMap.SetMapIndex(currentKey, currentVal)
	}

	val.Set(valMap)

	return nil
}

func (d *Conver) toMapFromStruct(name string, dataVal reflect.Value, val reflect.Value, valMap reflect.Value) error {
	typ := dataVal.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)

		fmt.Println(name, f.PkgPath, f.Name, dataVal.IsValid())
		if f.PkgPath != "" {
			continue
		}

		v := dataVal.Field(i)
		if !v.Type().AssignableTo(valMap.Type().Elem()) {
			return fmt.Errorf("cannot assign type '%s' to map value field of type '%s'", v.Type(), valMap.Type().Elem())
		}

		keyName, opt := zreflect.GetStructTag(f, d.TagName, tagNameLesser)
		if keyName == "" {
			continue
		}
		squash := d.Squash && v.Kind() == reflect.Struct && f.Anonymous

		if !(v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct) {
			nv := v.Elem()
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				keyName, _ := zreflect.GetStructTag(f, d.TagName, tagNameLesser)
				if keyName != "" {
					v = nv
					break
				}
			}
		}

		if opt != "" {
			if strings.Index(opt, "omitempty") != -1 && !zreflect.Nonzero(v) {
				continue
			}

			squash = squash || strings.Index(opt, "squash") != -1
			if squash {
				if v.Kind() == reflect.Ptr && v.Elem().Kind() == reflect.Struct {
					v = v.Elem()
				}
				if v.Kind() != reflect.Struct {
					return fmt.Errorf("cannot squash non-struct type '%s'", v.Type())
				}
			}
		}

		switch v.Kind() {
		case reflect.Struct:
			x := reflect.New(v.Type())
			x.Elem().Set(v)
			vType := valMap.Type()
			vKeyType := vType.Key()
			vElemType := vType.Elem()
			mType := reflect.MapOf(vKeyType, vElemType)
			vMap := reflect.MakeMap(mType)
			addrVal := reflect.New(vMap.Type())
			reflect.Indirect(addrVal).Set(vMap)

			err := d.to(keyName, x.Interface(), reflect.Indirect(addrVal))
			if err != nil {
				return err
			}

			vMap = reflect.Indirect(addrVal)
			if squash {
				for _, k := range vMap.MapKeys() {
					valMap.SetMapIndex(k, vMap.MapIndex(k))
				}
			} else {
				valMap.SetMapIndex(zreflect.ValueOf(keyName), vMap)
			}

		default:
			valMap.SetMapIndex(zreflect.ValueOf(keyName), v)
		}
	}

	if val.CanAddr() {
		val.Set(valMap)
	}

	return nil
}

func (d *Conver) toPtr(name string, data interface{}, val reflect.Value) error {
	isNil := data == nil
	if !isNil {
		switch v := reflect.Indirect(zreflect.ValueOf(data)); v.Kind() {
		case reflect.Chan,
			reflect.Func,
			reflect.Interface,
			reflect.Map,
			reflect.Ptr,
			reflect.Slice:
			isNil = v.IsNil()
		}
	}

	if isNil {
		if !val.IsNil() && val.CanSet() {
			nilValue := reflect.New(val.Type()).Elem()
			val.Set(nilValue)
		}

		return nil
	}

	valType := val.Type()
	valElemType := valType.Elem()
	if val.CanSet() {
		realVal := val
		if realVal.IsNil() || d.ZeroFields {
			realVal = reflect.New(valElemType)
		}

		if err := d.to(name, data, reflect.Indirect(realVal)); err != nil {
			return err
		}

		val.Set(realVal)
	} else {
		if err := d.to(name, data, reflect.Indirect(val)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Conver) toSlice(name string, data interface{}, val reflect.Value) error {
	dataVal := reflect.Indirect(zreflect.ValueOf(data))
	dataValKind := dataVal.Kind()
	valType := val.Type()
	valElemType := valType.Elem()
	sliceType := reflect.SliceOf(valElemType)

	if dataValKind != reflect.Array && dataValKind != reflect.Slice {
		switch {
		case dataValKind == reflect.Slice, dataValKind == reflect.Array:
			break
		case dataValKind == reflect.Map:
			if dataVal.Len() == 0 {
				val.Set(reflect.MakeSlice(sliceType, 0, 0))
				return nil
			}
			return d.toSlice(name, []interface{}{data}, val)
		case dataValKind == reflect.String && valElemType.Kind() == reflect.Uint8:
			return d.toSlice(name, []byte(dataVal.String()), val)
		default:
			return d.toSlice(name, []interface{}{data}, val)
		}
	}

	if dataValKind != reflect.Array && dataVal.IsNil() {
		return nil
	}

	valSlice := val
	if valSlice.IsNil() || d.ZeroFields {
		valSlice = reflect.MakeSlice(sliceType, dataVal.Len(), dataVal.Len())
	} else if valSlice.Len() > dataVal.Len() {
		valSlice = valSlice.Slice(0, dataVal.Len())
	}

	for i := 0; i < dataVal.Len(); i++ {
		currentData := dataVal.Index(i).Interface()
		for valSlice.Len() <= i {
			valSlice = reflect.Append(valSlice, reflect.Zero(valElemType))
		}
		currentField := valSlice.Index(i)
		fieldName := name + "[" + strconv.Itoa(i) + "]"
		if err := d.to(fieldName, currentData, currentField); err != nil {
			return err
		}
	}

	val.Set(valSlice)

	return nil
}

func (d *Conver) toArray(name string, data interface{}, val reflect.Value) error {
	dataVal := reflect.Indirect(zreflect.ValueOf(data))
	dataValKind, valType := dataVal.Kind(), val.Type()
	valElemType := valType.Elem()
	arrayType, valArray := reflect.ArrayOf(valType.Len(), valElemType), val
	if valArray.Interface() == reflect.Zero(valArray.Type()).Interface() || d.ZeroFields {
		if dataValKind != reflect.Array && dataValKind != reflect.Slice {
			switch {
			case dataValKind == reflect.Map:
				if dataVal.Len() == 0 {
					val.Set(reflect.Zero(arrayType))
					return nil
				}
			default:
				return d.toArray(name, []interface{}{data}, val)
			}
		}
		if dataVal.Len() > arrayType.Len() {
			return fmt.Errorf(
				"'%s': expected source data to have length less or equal to %d, got %d", name, arrayType.Len(), dataVal.Len())
		}

		valArray = reflect.New(arrayType).Elem()
	}

	for i := 0; i < dataVal.Len(); i++ {
		currentData := dataVal.Index(i).Interface()
		currentField := valArray.Index(i)

		fieldName := name + "[" + strconv.Itoa(i) + "]"
		if err := d.to(fieldName, currentData, currentField); err != nil {
			return err
		}
	}

	val.Set(valArray)

	return nil
}

func (d *Conver) toFunc(name string, data interface{}, val reflect.Value) error {
	dataVal := reflect.Indirect(zreflect.ValueOf(data))
	if val.Type() != dataVal.Type() {
		return fmt.Errorf(
			"'%s' expected type '%s', got unconvertible type '%s', value: '%v'",
			name, val.Type(), dataVal.Type(), data)
	}
	val.Set(dataVal)
	return nil
}
