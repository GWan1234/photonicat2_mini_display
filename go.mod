module photonicat2_display

go 1.19

require (
	github.com/go-ping/ping v1.2.0
	github.com/llgcode/draw2d v0.0.0-20240627062922-0ed1ff131195
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef
	golang.org/x/image v0.24.0
	periph.io/x/conn/v3 v3.7.2
	periph.io/x/host/v3 v3.8.3
	photonicat2_display/periph.io-st7789 v0.0.0-00010101000000-000000000000
)

require (
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/uuid v1.2.0 // indirect
	golang.org/x/net v0.0.0-20211118161319-6a13c67c3ce4 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.0.0-20210423082822-04245dca01da // indirect
	golang.org/x/text v0.22.0 // indirect
)

replace photonicat2_display/periph.io-st7789 => ./periph.io-st7789
