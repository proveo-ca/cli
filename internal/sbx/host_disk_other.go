//go:build !unix

package sbx

// diskFree reports no reading where proveo has no free-space probe.
func diskFree(string) (uint64, bool) { return 0, false }

func diskSpace(string) (uint64, uint64, bool) { return 0, 0, false }
