package cli

// takeBoolFlag — ro'yxatdan bitta bayroqni (masalan "-w") olib tashlaydi va
// bor-yo'qligini qaytaradi.
func takeBoolFlag(args []string, name string) (found bool, rest []string) {
	rest = make([]string, 0, len(args))
	for _, a := range args {
		if a == name {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return found, rest
}

// takeValueFlag — "-n 50" yoki "-n=50" ko'rinishidagi bayroqni oladi.
func takeValueFlag(args []string, name string) (value string, found bool, rest []string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == name && i+1 < len(args) {
			value, found = args[i+1], true
			i++
			continue
		}
		rest = append(rest, a)
	}
	return value, found, rest
}
