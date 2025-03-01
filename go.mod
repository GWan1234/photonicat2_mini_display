module photonicat2_display

go 1.19

require (
	periph.io/x/conn/v3 v3.7.2
	periph.io/x/host/v3 v3.8.3
	photonicat2_display/periph.io-st7789 v0.0.0-00010101000000-000000000000
)

replace photonicat2_display/periph.io-st7789 => ./periph.io-st7789
