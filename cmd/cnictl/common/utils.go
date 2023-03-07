package common

import (
	"encoding/json"
	"fmt"
)

func ShowObject(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		OnError(err)
	}
	fmt.Println(string(data))
}
