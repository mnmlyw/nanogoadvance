// Package common — port of src/nba/include/nba/common/*.
//
// crc32.go ⇄ crc32.hh — the same single-bit IEEE 802.3 CRC-32 upstream
// uses to fingerprint the MP2K SoundMain() prologue. Slow but exact;
// adequate because we only call it during ROM scan, not per frame.
package common

func CRC32(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		for range 8 {
			if (crc^uint32(b))&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
			b >>= 1
		}
	}
	return ^crc
}
