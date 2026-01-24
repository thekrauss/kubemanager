package utils

import (
	"k8s.io/apimachinery/pkg/api/resource"
)

func IsSizeReduction(oldStr, newStr string) bool {
	oldQty := resource.MustParse(oldStr)
	newQty := resource.MustParse(newStr)
	return newQty.Cmp(oldQty) == -1 //  true si new < old
}

func ParseStorageToBytes(size string) int64 {
	res, err := resource.ParseQuantity(size)
	if err != nil {
		return 0
	}
	return res.Value()
}
