package utils

func SelectAvailableSubnet(subnets []string) string {
	if len(subnets) > 0 {
		return subnets[0]
	}
	return ""
}
