package util

import "fmt"

func IDFromAttachmentURL(url string) string {
	// Extracts the ID from a string like: https://cdn.stoatusercontent.com/avatars/0d_oHg1EDTnfeBNDMJGa_1GAdvVxPEpoWQSnyj-Oe3?max_side=256
	// should return just: "0d_oHg1EDTnfeBNDMJGa_1GAdvVxPEpoWQSnyj-Oe3"
	var start, slashes int

	for i := 0; i < len(url); i++ {
		if url[i] == '/' {
			slashes++
			if slashes == 4 {
				start = i + 1
				break
			}
		}
	}

	if start == 0 {
		return ""
	}

	for i := start; i < len(url); i++ {
		if url[i] == '?' {
			fmt.Println("Returning:", url[start:i])
			return url[start:i]
		}
	}

	return url[start:]
}
