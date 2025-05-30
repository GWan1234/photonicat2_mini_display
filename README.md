# photonicat2_display_go

A Go language-based, HTTP-enabled driver for the Photonicat 2 LCD display used in the Photonicat 2 mobile router.

## Overview

**photonicat2_display_go** is a software project written in Go that provides an HTTP-enabled driver for controlling an LCD display on the Photonicat 2 mobile router. The driver allows real-time display updates, configuration via HTTP endpoints, and integrates seamlessly with the underlying hardware using SPI and GPIO.

### Key Features

- **HTTP Server Integration:**  
  Control the display via HTTP GET/POST requests. Retrieve the current framebuffer image as a PNG and update display variables remotely.
  
- **Real-Time Display Updates:**  
  Efficient frame rendering with dynamic FPS monitoring and double-buffering for smooth updates.
  
- **Hardware Interfacing:**  
  Utilizes periph.io libraries for SPI and GPIO control to communicate with the LCD hardware.
  
- **Customizable Configuration:**  
  Easily configure display elements and settings via JSON configuration files.

## Hardware

- **Device:** Photonicat 2 mobile router  
- **LCD Display:** Photonicat 2 LCD  
- **Interfaces:** SPI, GPIO

## Software

- **Language:** Go (Golang)  
- **HTTP-Enabled Driver:** Provides a built-in web server for display control  
- **Dependencies:**
  - [periph.io](https://periph.io/) for hardware interfacing (SPI, GPIO)
  - [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) for image and font rendering

## Getting Started

### Prerequisites

- Go 1.16 or higher
- Proper hardware setup for the Photonicat 2 mobile router and LCD display
<code>
apt install gcc-aarch64-linux-gnu musl-tools
</code>

### Run
<code>
git clone https://github.com/yourusername/photonicat2_display_go.git
cd photonicat2_display_go
go mod tidy
go run .
</code>

## License
This project is licensed under the GNU General Public License v3 (GPL-3.0-only).
For more details, see GNU GPL v3.


Acknowledgements
periph.io for the hardware interfacing libraries.
The Go community for providing robust tooling and libraries.