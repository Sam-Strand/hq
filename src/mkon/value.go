// Package mkon — Go-реализация формата mkon, эквивалентная mkon-js (эталон) и mkon_py.
//
// Только стандартная библиотека. Модель данных повторяет JS:
//
//	{...}            *Object   (ключи в порядке вставки)
//	[...]            *Array
//	строка           string
//	число            float64   (в mkon числа — IEEE-754 double, как в JS; NaN, ±Inf, -0 — тоже)
//	2024-01-15       *Date     (момент времени в UTC, точность — миллисекунды, как у JS Date)
//	true / false     bool
//	null             nil
//
// Stringify дополнительно принимает int*, uint*, float32, time.Time, []any и map[string]any.
package mkon

import "time"

// Comments — комментарии узла, та же структура, что `__COMMENTS__` в mkon-js:
//
//	объект: C[ключ] = [до, после], O — порядок ключей с комментариями, E — внутри пустого;
//	массив: A[i] = [до, после], E — внутри пустого;
//	корень: R = [до, после].
type Comments struct {
	C    map[string][2]string
	O    []string
	A    [][2]string
	E    []string
	HasE bool
	R    [2]string
	HasR bool
}

// Object — объект mkon с порядком ключей.
type Object struct {
	keys     []string
	values   map[string]any
	Comments *Comments
}

// NewObject создаёт пустой объект.
func NewObject() *Object { return &Object{values: map[string]any{}} }

// Set задаёт значение; новый ключ добавляется в конец, существующий сохраняет позицию.
func (o *Object) Set(key string, v any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// Get возвращает значение по ключу.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

// Has сообщает, есть ли ключ.
func (o *Object) Has(key string) bool {
	_, ok := o.values[key]
	return ok
}

// Delete удаляет ключ.
func (o *Object) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// Keys — ключи в порядке вставки (копия).
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Len — число ключей.
func (o *Object) Len() int { return len(o.keys) }

// Array — массив mkon.
type Array struct {
	Items    []any
	Comments *Comments
}

// Date — ISO-дата mkon: момент времени (UTC, миллисекунды).
type Date struct {
	Time     time.Time
	Comments *Comments // только корневые комментарии (R), как у Date в mkon-js
}

// NewDate создаёт дату из time.Time (переводится в UTC и обрезается до миллисекунд, как в JS).
func NewDate(t time.Time) *Date {
	t = t.UTC()
	return &Date{Time: t.Truncate(time.Millisecond)}
}
