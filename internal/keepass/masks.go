package keepass

import (
	"fmt"
	"regexp"
	"strings"
)

var maskRe = regexp.MustCompile(`<([\w]+)\|([^>]*)>`) // путь может быть пустым (<password|>) — тоже ошибка

// ParseMask разбирает маску `<field|путь>`; поле — одно из легаси-полей
// (title/username/password/url), регистронезависимо.
func ParseMask(mask string) (field, path string, ok bool) {
	m := maskRe.FindStringSubmatch(mask)
	if m == nil {
		return "", "", false
	}
	field, path = strings.ToLower(m[1]), m[2]
	switch field {
	case "title", "username", "password", "url":
		if path != "" {
			return field, path, true
		}
	}
	return "", "", false
}

// ReplaceMasks заменяет все маски значениями из резолвера
// (например, пароль в поле подключения). M35-ревью (MAJOR-2): маска СИГНАЛИТСЯ
// ошибкой, если поле неизвестно или путь пуст (иначе литерал `<passwrod|…>`
// уходил бы как кред — дыра того же класса, что исправлял 6ec4e40).
func ReplaceMasks(input string, v FieldResolver) (string, error) {
	var firstErr error
	out := maskRe.ReplaceAllStringFunc(input, func(m string) string {
		field, path, ok := ParseMask(m)
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("маска %q: неизвестное поле или пустой путь", m)
			}
			return m
		}
		val, err := v.Field(field, path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return m
		}
		return val
	})
	if firstErr != nil {
		return out, firstErr
	}
	return out, nil
}
