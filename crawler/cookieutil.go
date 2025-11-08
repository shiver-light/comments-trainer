package main

import (
	"encoding/json"
	"net/http"
	"os"
)

func LoadCookiesFromFile(path string) []*http.Cookie {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var raw []simpleCookie
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil
	}
	var cookies []*http.Cookie
	for _, c := range raw {
		ck := &http.Cookie{
			Name:  c.Name,
			Value: c.Value,
			Path:  "/",
		}
		if c.Domain != "" {
			ck.Domain = c.Domain
		}
		if c.Path != "" {
			ck.Path = c.Path
		}
		cookies = append(cookies, ck)
	}
	return cookies
}

// func LoadCookiesFromFile(path string) []*http.Cookie {
// 	f, err := os.Open(path)
// 	if err != nil {
// 		return nil
// 	}
// 	defer f.Close()
// 	var raw []map[string]interface{}
// 	if err := json.NewDecoder(f).Decode(&raw); err != nil {
// 		return nil
// 	}
// 	var cookies []*http.Cookie
// 	for _, c := range raw {
// 		ck := &http.Cookie{
// 			Name:  c["name"].(string),
// 			Value: c["value"].(string),
// 			Path:  "/",
// 		}
// 		if dom, ok := c["domain"].(string); ok {
// 			ck.Domain = dom
// 		}
// 		if p, ok := c["path"].(string); ok {
// 			ck.Path = p
// 		}
// 		cookies = append(cookies, ck)
// 	}
// 	return cookies
// }
