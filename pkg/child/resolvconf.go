package child

func generateResolvConf(dns string) []byte {
	return []byte("nameserver " + dns + "\n")
}
