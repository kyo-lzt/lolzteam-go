package lolzteam

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// StringOrInt represents a value that can be either a string or an integer.
type StringOrInt struct {
	StringValue *string
	IntValue    *int64
}

func (s StringOrInt) MarshalJSON() ([]byte, error) {
	if s.IntValue != nil {
		return json.Marshal(*s.IntValue)
	}
	if s.StringValue != nil {
		return json.Marshal(*s.StringValue)
	}
	return []byte("null"), nil
}

func (s *StringOrInt) UnmarshalJSON(data []byte) error {
	var i int64
	if err := json.Unmarshal(data, &i); err == nil {
		s.IntValue = &i
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.StringValue = &str
		return nil
	}
	return fmt.Errorf("StringOrInt: cannot unmarshal %s", string(data))
}

func (s StringOrInt) String() string {
	if s.IntValue != nil {
		return strconv.FormatInt(*s.IntValue, 10)
	}
	if s.StringValue != nil {
		return *s.StringValue
	}
	return ""
}
