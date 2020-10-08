package util

import (
	"math/rand"
	"time"
)

func GenPassword(pwdLen int) (pwd string) {
	rand.Seed(time.Now().Unix())
	var cs = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	pwdSlice := make([]byte, pwdLen)
	for idx := range pwdSlice {
		pwdSlice[idx] = cs[rand.Int()%len(cs)]
	}
	return string(pwdSlice)
}
