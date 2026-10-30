package sitecontent

import (
	"reflect"
	"strconv"
	"strings"
)

// Expand returns a copy of c with {years} replaced by
// identity.experience_years in every text field. Links, video IDs and the
// YouTube handle are left as they are.
func Expand(c Content) Content {
	out := clone(c)
	expand(reflect.ValueOf(&out).Elem(), "", strconv.Itoa(out.Identity.ExperienceYears))
	return out
}

func expand(v reflect.Value, key, years string) {
	switch v.Kind() {
	case reflect.String:
		if !rawField(key) {
			v.SetString(strings.ReplaceAll(v.String(), "{years}", years))
		}
	case reflect.Slice:
		for i := range v.Len() {
			expand(v.Index(i), key, years)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			expand(v.Field(i), jsonKey(v.Type().Field(i)), years)
		}
	}
}

func rawField(key string) bool {
	return key == "url" || strings.HasSuffix(key, "_url") || key == "youtube_id" || key == "youtube_handle"
}
