package docker

import "strconv"

func formatPort(ip string, public, private uint16, proto string) string {
	host := ip
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = ""
	} else {
		host += ":"
	}
	return host + strconv.Itoa(int(public)) + "→" + strconv.Itoa(int(private)) + "/" + proto
}

func formatPrivate(private uint16, proto string) string {
	return strconv.Itoa(int(private)) + "/" + proto
}
