package utils

import (
	"fmt"

	"github.com/go-redis/redis"
)

var client = redis.NewClient(&redis.Options{
	Addr:     "localhost:6379",
	Password: "",
	DB:       1,
})

func PingRedis() {
	pong, err := client.Ping().Result()
	fmt.Println("Ping redis")
	fmt.Println(pong, err)
}

func SaveToRedis(key string, value interface{}) bool {
	err := client.Set(key, value, 0).Err()
	if err != nil {
		fmt.Println("SaveToRedis")
		fmt.Println(err)
		return false
	}
	fmt.Println("SaveToRedis success")
	return true
}

func ReadSingle(key string) string {
	val, err := client.Get(key).Result()
	if err != nil {
		fmt.Println(err)
		return ""
	}
	fmt.Println(val)
	return val
}

func ReadMulti(keyPattern string) []interface{} {
	keys, err := client.Keys(keyPattern).Result()
	if err != nil {
		fmt.Println(err)
		return nil
	}
	if len(keys) == 0 {
		return nil
	}
	vals, err2 := client.MGet(keys...).Result()
	if err2 != nil {
		fmt.Println(err2)
		return nil
	}
	return vals
}

func DeleteSingle(key string) bool {
	_, err := client.Del(key).Result()
	if err != nil {
		fmt.Println(err)
		return false
	}

	return true
}

func FlushDB() {
	_, err := client.FlushDB().Result()
	if err != nil {
		fmt.Println("Error")
		return
	}
	fmt.Println("FlushDB success")
}
