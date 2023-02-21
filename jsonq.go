package gojsonq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"reflect"
	"strconv"
	"strings"
)

// New returns a new instance of JSONQ
func New(options ...OptionFunc) *JSONQ {
	jq := &JSONQ{
		queryMap: loadDefaultQueryMap(),
		option: option{
			decoder: &DefaultDecoder{},
		},
	}
	for _, option := range options {
		if err := option(jq); err != nil {
			jq.addError(err)
		}
	}
	return jq
}

// empty represents an empty result
var empty interface{}

// query describes a query
type query struct {
	key, operator string
	value         interface{}
}

// JSONQ describes a JSONQ type which contains all the state
type JSONQ struct {
	option           option               // contains options for JSONQ
	queryMap         map[string]QueryFunc // contains query functions
	node             string               // contains node name
	raw              json.RawMessage      // raw message from source (reader, string or file)
	rootJSONContent  interface{}          // original decoded json data
	jsonContent      interface{}          // copy of original decoded json data for further processing
	queryIndex       int                  // contains number of orWhere query call
	queries          [][]query            // nested queries
	attributes       []string             // select attributes that will be available in final resuls
	limitRecords     int                  // number of records that willbe available in final result
	distinctProperty string               // contain the distinct attribute name
	errors           []error              // contains all the errors when processing
}

// String satisfies stringer interface
func (j *JSONQ) String() string {
	return fmt.Sprintf("\nContent: %s\nQueries:%v\n", string(j.raw), j.queries)
}

// decode decodes the raw message to Go data structure
func (j *JSONQ) decode() *JSONQ {
	if err := j.option.decoder.
		Decode(j.raw, &j.rootJSONContent); err != nil {
		return j.addError(err)
	}
	j.jsonContent = j.rootJSONContent
	return j
}

// Copy returns a new fresh instance of JSONQ with the original copy of data so that you can do
// concurrent operation on the same data without being decoded again
func (j *JSONQ) Copy() *JSONQ {
	tmp := *j
	return tmp.reset()
}

// File read the json content from physical file
func (j *JSONQ) File(filename string) *JSONQ {
	bb, err := ioutil.ReadFile(filename)
	if err != nil {
		return j.addError(err)
	}
	j.raw = bb
	return j.decode() // handle error
}

// JSONString reads the json content from valid json string
func (j *JSONQ) JSONString(json string) *JSONQ {
	j.raw = []byte(json)
	return j.decode() // handle error
}

// Reader reads the json content from io reader
func (j *JSONQ) Reader(r io.Reader) *JSONQ {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r)
	if err != nil {
		return j.addError(err)
	}
	j.raw = buf.Bytes()
	buf.Reset() // reset the buffer
	return j.decode()
}

// Error returns first occurred error
func (j *JSONQ) Error() error {
	errsln := len(j.errors)
	if errsln == 0 {
		return nil
	}
	return j.errors[0]
}

// Errors returns list of all errors
func (j *JSONQ) Errors() []error {
	return j.errors
}

// addError adds error to error list
func (j *JSONQ) addError(err error) *JSONQ {
	j.errors = append(j.errors, fmt.Errorf("gojsonq: %v", err))
	return j
}

// Macro adds a new query func to the JSONQ
func (j *JSONQ) Macro(operator string, fn QueryFunc) *JSONQ {
	if _, ok := j.queryMap[operator]; ok {
		j.addError(fmt.Errorf("%s is already registered in query map", operator))
		return j
	}
	j.queryMap[operator] = fn
	return j
}

// From seeks the json content to provided node. e.g: "users.[0]"  or "users.[0].name"
func (j *JSONQ) From(node string) *JSONQ {
	j.node = node
	v, err := getNestedValue(j.jsonContent, node)
	if err != nil {
		j.addError(err)
	}
	j.jsonContent = v
	return j
}

// Select use for selection of the properties from query result
func (j *JSONQ) Select(properties ...string) *JSONQ {
	j.attributes = append(j.attributes, properties...)
	return j
}

// Limit limits the number of records in result
func (j *JSONQ) Limit(limit int) *JSONQ {
	j.limitRecords = limit
	return j
}

// limit return the number of records in result set depending on the limit value
func (j *JSONQ) limit() *JSONQ {
	if list, ok := j.jsonContent.([]interface{}); ok {
		if j.limitRecords <= 0 {
			j.addError(fmt.Errorf("%d is invalid limit", j.limitRecords))
			return j
		}
		if len(list) > j.limitRecords {
			j.jsonContent = list[:j.limitRecords]
		}
	}
	return j
}

// Where builds a where clause. e.g: Where("name", "contains", "doe")
func (j *JSONQ) Where(key, cond string, val interface{}) *JSONQ {
	q := query{
		key:      key,
		operator: cond,
		value:    val,
	}
	if j.queryIndex == 0 && len(j.queries) == 0 {
		qq := []query{}
		qq = append(qq, q)
		j.queries = append(j.queries, qq)
	} else {
		j.queries[j.queryIndex] = append(j.queries[j.queryIndex], q)
	}

	return j
}

// WhereEqual is an alias of Where("key", "=", val)
func (j *JSONQ) WhereEqual(key string, val interface{}) *JSONQ {
	return j.Where(key, signEq, val)
}

// WhereNotEqual is an alias of Where("key", "!=", val)
func (j *JSONQ) WhereNotEqual(key string, val interface{}) *JSONQ {
	return j.Where(key, signNotEq, val)
}

// WhereNil is an alias of Where("key", "=", nil)
func (j *JSONQ) WhereNil(key string) *JSONQ {
	return j.Where(key, signEq, nil)
}

// WhereNotNil is an alias of Where("key", "!=", nil)
func (j *JSONQ) WhereNotNil(key string) *JSONQ {
	return j.Where(key, signNotEq, nil)
}

// WhereIn is an alias for where("key", "in", []string{"a", "b"})
func (j *JSONQ) WhereIn(key string, val interface{}) *JSONQ {
	j.Where(key, signIn, val)
	return j
}

// WhereNotIn is an alias for where("key", "notIn", []string{"a", "b"})
func (j *JSONQ) WhereNotIn(key string, val interface{}) *JSONQ {
	j.Where(key, signNotIn, val)
	return j
}

// OrWhere builds an OrWhere clause, basically it's a group of AND clauses
func (j *JSONQ) OrWhere(key, cond string, val interface{}) *JSONQ {
	j.queryIndex++
	qq := []query{}
	qq = append(qq, query{
		key:      key,
		operator: cond,
		value:    val,
	})
	j.queries = append(j.queries, qq)
	return j
}

// WhereStartsWith satisfies Where clause which starts with provided value(string)
func (j *JSONQ) WhereStartsWith(key string, val interface{}) *JSONQ {
	return j.Where(key, signStartsWith, val)
}

// WhereEndsWith satisfies Where clause which ends with provided value(string)
func (j *JSONQ) WhereEndsWith(key string, val interface{}) *JSONQ {
	return j.Where(key, signEndsWith, val)
}

// WhereContains satisfies Where clause which contains provided value(string)
func (j *JSONQ) WhereContains(key string, val interface{}) *JSONQ {
	return j.Where(key, signContains, val)
}

// WhereStrictContains satisfies Where clause which contains provided value(string).
// This is case sensitive
func (j *JSONQ) WhereStrictContains(key string, val interface{}) *JSONQ {
	return j.Where(key, signStrictContains, val)
}

// WhereLenEqual is an alias of Where("key", "leneq", val)
func (j *JSONQ) WhereLenEqual(key string, val interface{}) *JSONQ {
	return j.Where(key, signLenEq, val)
}

// WhereLenNotEqual is an alias of Where("key", "lenneq", val)
func (j *JSONQ) WhereLenNotEqual(key string, val interface{}) *JSONQ {
	return j.Where(key, signLenNotEq, val)
}

// findInArray traverses through a list and returns the value list.
// This helps to process Where/OrWhere queries
func (j *JSONQ) findInArray(aa []interface{}) []interface{} {
	result := make([]interface{}, 0)
	for _, a := range aa {
		if m, ok := a.(map[string]interface{}); ok {
			result = append(result, j.findInMap(m)...)
		}
	}
	return result
}

// findInMap traverses through a map and returns the matched value list.
// This helps to process Where/OrWhere queries
func (j *JSONQ) findInMap(vm map[string]interface{}) []interface{} {
	result := make([]interface{}, 0)
	orPassed := false
	for _, qList := range j.queries {
		andPassed := true
		for _, q := range qList {
			cf, ok := j.queryMap[q.operator]
			if !ok {
				j.addError(fmt.Errorf("invalid operator %s", q.operator))
				return result
			}
			nv, errnv := getNestedValue(vm, q.key)
			if errnv != nil {
				j.addError(errnv)
				andPassed = false
			} else {
				// change to type of data
				if reflect.TypeOf(nv) != reflect.TypeOf(q.value) {
					if reflect.TypeOf(nv).String() == "float64" {
						if item, ok := q.value.(string); ok {
							// q.value = VersionOrdinal(item) //TODO check how it working for string
							f, err := strconv.ParseFloat(VersionOrdinal(item), 64)
							if err == nil {
								q.value = f
							}
						}

					}
				}
				qb, err := cf(nv, q.value)
				if err != nil {
					j.addError(err)
				}
				andPassed = andPassed && qb
			}
		}
		orPassed = orPassed || andPassed
	}
	if orPassed {
		result = append(result, vm)
	}
	return result
}

// VersionOrdinal convert string to version
func VersionOrdinal(version string) string {
	if strings.Contains(version, ".") {
		temp := strings.Split(version, ".")
		return fmt.Sprintf("%s.%s", temp[0], strings.Join(temp[0:], ""))
	}
	return version
}

// processQuery makes the result
func (j *JSONQ) processQuery() *JSONQ {
	if aa, ok := j.jsonContent.([]interface{}); ok {
		j.jsonContent = j.findInArray(aa)
	}
	return j
}

// prepare builds the queries
func (j *JSONQ) prepare() *JSONQ {
	if len(j.queries) > 0 {
		j.processQuery()
	}
	j.queryIndex = 0
	return j
}

// GroupBy builds a chunk of exact matched data in a group list using provided attribute/column/property
func (j *JSONQ) GroupBy(property string) *JSONQ {
	j.prepare()

	dt := map[string][]interface{}{}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, a := range aa {
			if vm, ok := a.(map[string]interface{}); ok {
				v, err := getNestedValue(vm, property)
				if err != nil {
					j.addError(err)
				} else {
					dt[toString(v)] = append(dt[toString(v)], vm)
				}
			}
		}
	}
	// replace the new result with the previous result
	j.jsonContent = dt
	return j
}

// Sort sorts an array
// default ascending order, pass "desc" for descending order
func (j *JSONQ) Sort(order ...string) *JSONQ {
	j.prepare()

	asc := true
	if len(order) > 1 {
		return j.addError(fmt.Errorf("sort accepts only one argument asc/desc"))
	}
	if len(order) > 0 && order[0] == "desc" {
		asc = false
	}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		j.jsonContent = sortList(arr, asc)
	}
	return j
}

// SortBy sorts an array
// default ascending order, pass "desc" for descending order
func (j *JSONQ) SortBy(order ...string) *JSONQ {
	j.prepare()
	asc := true
	if len(order) == 0 {
		return j.addError(fmt.Errorf("provide at least one argument as property name"))
	}
	if len(order) > 2 {
		return j.addError(fmt.Errorf("sort accepts only two arguments. first argument property name and second argument asc/desc"))
	}

	if len(order) > 1 && order[1] == "desc" {
		asc = false
	}

	return j.sortBy(order[0], asc)
}

// Distinct builds distinct value using provided attribute/column/property
func (j *JSONQ) Distinct(property string) *JSONQ {
	j.distinctProperty = property
	return j
}

// distinct builds distinct value using provided attribute/column/property
func (j *JSONQ) distinct() *JSONQ {
	m := map[string]bool{}
	dt := []interface{}{}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, a := range aa {
			if vm, ok := a.(map[string]interface{}); ok {
				v, err := getNestedValue(vm, j.distinctProperty)
				if err != nil {
					j.addError(err)
				} else {
					if _, exist := m[toString(v)]; !exist {
						dt = append(dt, vm)
						m[toString(v)] = true
					}
				}
			}
		}
	}
	// replace the new result with the previous result
	j.jsonContent = dt
	return j
}

// sortBy sorts list of map
func (j *JSONQ) sortBy(property string, asc bool) *JSONQ {
	sortResult, ok := j.jsonContent.([]interface{})
	if !ok {
		return j
	}
	if len(sortResult) == 0 {
		return j
	}

	sm := &sortMap{}
	sm.key = property
	if !asc {
		sm.desc = true
	}
	sm.Sort(sortResult)

	for _, e := range sm.errs {
		j.addError(e)
	}

	// replace the new result with the previous result
	j.jsonContent = sortResult
	return j
}

// only return selected properties in result
func (j *JSONQ) only(properties ...string) interface{} {
	result := []interface{}{}
	if j.distinctProperty != "" {
		j.distinct()
	}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, am := range aa {
			tmap := map[string]interface{}{}
			for _, prop := range properties {
				node, alias := makeAlias(prop)
				rv, errV := getNestedValue(am, node)
				if errV != nil {
					j.addError(errV)
					continue
				}
				tmap[alias] = rv
			}
			if len(tmap) > 0 {
				result = append(result, tmap)
			}
		}
	}
	return result
}

// Only collects the properties from a list of object
func (j *JSONQ) Only(properties ...string) interface{} {
	return j.prepare().only(properties...)
}

// Pluck build an array of vlaues form a property of a list of objects
func (j *JSONQ) Pluck(property string) interface{} {
	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	if j.limitRecords != 0 {
		j.limit()
	}
	result := []interface{}{}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, am := range aa {
			if mv, ok := am.(map[string]interface{}); ok {
				if v, ok := mv[property]; ok {
					result = append(result, v)
				}
			}
		}
	}
	return result
}

// reset resets the current state of JSONQ instance
func (j *JSONQ) reset() *JSONQ {
	j.jsonContent = j.rootJSONContent
	j.node = ""
	j.queries = make([][]query, 0)
	j.attributes = make([]string, 0)
	j.queryIndex = 0
	j.limitRecords = 0
	j.distinctProperty = ""
	return j
}

// Reset resets the current state of JSON instance and make a fresh object with the original json content
func (j *JSONQ) Reset() *JSONQ {
	return j.reset()
}

// Get return the result
func (j *JSONQ) Get() interface{} {
	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	if j.limitRecords != 0 {
		j.limit()
	}
	if len(j.attributes) > 0 {
		return j.only(j.attributes...)
	}
	return j.jsonContent
}

// First returns the first element of a list
func (j *JSONQ) First() interface{} {
	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		if len(arr) > 0 {
			return arr[0]
		}
	}
	return empty
}

// Last returns the last element of a list
func (j *JSONQ) Last() interface{} {
	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		if l := len(arr); l > 0 {
			return arr[l-1]
		}
	}
	return empty
}

// Nth returns the nth element of a list
func (j *JSONQ) Nth(index int) interface{} {
	if index == 0 {
		j.addError(fmt.Errorf("index is not zero based"))
		return empty
	}

	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		alen := len(arr)
		if alen == 0 {
			j.addError(fmt.Errorf("list is empty"))
			return empty
		}
		if abs(index) > alen {
			j.addError(fmt.Errorf("index out of range"))
			return empty
		}
		if index > 0 {
			return arr[index-1]
		}
		return arr[alen+index]
	}
	return empty
}

// Find returns the result of a exact matching path
func (j *JSONQ) Find(path string) interface{} {
	return j.From(path).Get()
}

// Count returns the number of total items.
// This could be a length of list/array/map
func (j *JSONQ) Count() int {
	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	lnth := 0
	// list of items
	if list, ok := j.jsonContent.([]interface{}); ok {
		lnth = len(list)
	}
	// return map len // TODO: need to think about map
	if m, ok := j.jsonContent.(map[string]interface{}); ok {
		lnth = len(m)
	}
	// group data items
	if m, ok := j.jsonContent.(map[string][]interface{}); ok {
		lnth = len(m)
	}

	return lnth
}

// Out write the queried data to defined custom type
func (j *JSONQ) Out(v interface{}) {
	data, err := json.Marshal(j.Get())
	if err != nil {
		j.addError(err)
		return
	}
	if err := json.Unmarshal(data, &v); err != nil {
		j.addError(err)
	}
}

// getFloatValFromArray returns a list of float64 values from array/map for aggregation
func (j *JSONQ) getFloatValFromArray(arr []interface{}, property ...string) []float64 {
	ff := []float64{}
	for _, a := range arr {
		if av, ok := a.(float64); ok {
			if len(property) > 0 {
				j.addError(fmt.Errorf("unnecessary property name for array"))
				return nil
			}
			ff = append(ff, av)
		}
		if mv, ok := a.(map[string]interface{}); ok {
			if len(property) == 0 {
				j.addError(fmt.Errorf("property name can not be empty for object"))
				return nil
			}
			if fi, ok := mv[property[0]]; ok {
				if flt, ok := fi.(float64); ok {
					ff = append(ff, flt)
				} else {
					j.addError(fmt.Errorf("property %s's value '%v' is not numeric", property[0], fi))
					return nil
				}
			} else {
				j.addError(fmt.Errorf("property '%s' does not exist", property[0]))
				return nil
			}
		}
	}

	return ff
}

// getAggregationValues returns a list of float64 values for aggregation
func (j *JSONQ) getAggregationValues(property ...string) []float64 {
	j.prepare()
	if j.distinctProperty != "" {
		j.distinct()
	}
	if j.limitRecords != 0 {
		j.limit()
	}

	ff := []float64{}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		ff = j.getFloatValFromArray(arr, property...)
	}

	if mv, ok := j.jsonContent.(map[string]interface{}); ok {
		if len(property) == 0 {
			j.addError(fmt.Errorf("property can not be empty for object"))
			return nil
		}
		if fi, ok := mv[property[0]]; ok {
			if flt, ok := fi.(float64); ok {
				ff = append(ff, flt)
			} else {
				j.addError(fmt.Errorf("property %s's value '%v' is not numeric", property[0], fi))
				return nil
			}
		} else {
			j.addError(fmt.Errorf("property '%s' does not exist", property[0]))
			return nil
		}
	}
	return ff
}

// Sum returns sum of values from array or from map using property
func (j *JSONQ) Sum(property ...string) float64 {
	var sum float64
	for _, flt := range j.getAggregationValues(property...) {
		sum += flt
	}
	return sum
}

// Avg returns average of values from array or from map using property
func (j *JSONQ) Avg(property ...string) float64 {
	var sum float64
	fl := j.getAggregationValues(property...)
	for _, flt := range fl {
		sum += flt
	}
	return sum / float64(len(fl))
}

// Min returns minimum value from array or from map using property
func (j *JSONQ) Min(property ...string) float64 {
	var min float64
	flist := j.getAggregationValues(property...)
	if len(flist) > 0 {
		min = flist[0]
	}
	for _, flt := range flist {
		if flt < min {
			min = flt
		}
	}
	return min
}

// Max returns maximum value from array or from map using property
func (j *JSONQ) Max(property ...string) float64 {
	var max float64
	flist := j.getAggregationValues(property...)
	if len(flist) > 0 {
		max = flist[0]
	}
	for _, flt := range flist {
		if flt > max {
			max = flt
		}
	}
	return max
}
