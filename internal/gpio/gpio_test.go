package gpio

import "testing"

// TestDecimalToBCD covers the integer→BCD encoding the RTC uses for
// year/month/day/hour/minute/second registers.
func TestDecimalToBCD(t *testing.T) {
	cases := []struct {
		in   uint8
		want uint8
	}{
		{0, 0x00},
		{1, 0x01},
		{9, 0x09},
		{10, 0x10},
		{12, 0x12},
		{42, 0x42},
		{59, 0x59},
		{99, 0x99},
		// Year encoding uses (year - 2000), so e.g. 2026 → 26 → 0x26.
		{26, 0x26},
	}
	for _, c := range cases {
		got := decimalToBCD(c.in)
		if got != c.want {
			t.Errorf("decimalToBCD(%d) = %#02x, want %#02x", c.in, got, c.want)
		}
	}
}

// TestSolarSensorClkAdvancesCounter — confirms the solar-sensor
// pin-driven counter advances on CLK falling edges (the protocol cart
// games use to read brightness from Boktai-style carts). The GBA must
// have configured CLK + RST pins as outputs (DirOut), so we set the
// direction mask first.
func TestSolarSensorClkAdvancesCounter(t *testing.T) {
	ss := NewSolarSensor()
	ss.Reset()
	ss.SetPortDirections((1 << solarPinCLK) | (1 << solarPinRST))
	ss.SetLightLevel(0x60)
	start := ss.counter
	// Pulse CLK low→high→low; counter should tick on a falling edge.
	ss.Write(0)
	ss.Write(1 << solarPinCLK)
	ss.Write(0)
	if ss.counter == start {
		t.Errorf("solar sensor counter didn't advance on CLK falling edge: %d", ss.counter)
	}
}

// TestSolarSensorResetClearsCounter — RST asserted resets the counter.
func TestSolarSensorResetClearsCounter(t *testing.T) {
	ss := NewSolarSensor()
	ss.Reset()
	ss.SetPortDirections((1 << solarPinCLK) | (1 << solarPinRST))
	ss.SetLightLevel(0x80)
	for range 5 {
		ss.Write(1 << solarPinCLK)
		ss.Write(0)
	}
	if ss.counter == 0 {
		t.Fatalf("setup precondition: counter should have ticked above 0; got %d", ss.counter)
	}
	ss.Write(1 << solarPinRST)
	if ss.counter != 0 {
		t.Errorf("RST didn't reset counter: still at %d", ss.counter)
	}
}
