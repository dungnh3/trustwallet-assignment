package utils

import (
	"strconv"
)

func ConvertHexToDec(hexString string) int {
	decimalInt, err := strconv.ParseInt(hexString, 0, 64)
	if err != nil {
		return 0
	}
	return int(decimalInt)
}
