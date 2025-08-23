# Phonicat Screen Configuration Programming Guide

This guide will help you understand how to customize the OLED display content on your Phonicat device.

## 📋 Overview

Phonicat devices support fully customizable screen display content through JSON configuration files. You can configure multiple pages, each containing different elements such as text, icons, and fixed text.

## 🔧 Basic Configuration Structure

### Top-level Configuration

```json
{
  "screen_dimmer_time_on_battery_seconds": 60,
  "screen_dimmer_time_on_dc_seconds": 86400,
  "screen_max_brightness": 100,
  "screen_min_brightness": 0,
  "ping_site0": "taobao.com",
  "ping_site1": "photonicat.com",
  "show_sms": true,
  "sms_limit_for_screen": 5,
  "template": {
    "page0": [...],
    "page1": [...],
    "page2": [...],
    "page3": [...]
  }
}
```

**Configuration Parameters:**

- `screen_dimmer_time_on_battery_seconds`: Auto-dim time on battery (seconds)
- `screen_dimmer_time_on_dc_seconds`: Auto-dim time when charging (seconds)
- `screen_max_brightness`: Maximum brightness (0-100)
- `screen_min_brightness`: Minimum brightness (0-100)
- `ping_site0`, `ping_site1`: Websites used for network latency testing
- `show_sms`: Whether to show SMS functionality
- `sms_limit_for_screen`: SMS display limit on screen
- `template`: Screen page template configuration

## 📄 Pages and Element Configuration

### Page Structure

Each page (`page0`, `page1`, etc.) contains an array of elements:

```json
"page0": [
  {
    "type": "text",
    "x": 10,
    "y": 10,
    "font": "DejaVuSans12",
    "color": "white",
    "data_key": "uptime"
  },
  {
    "type": "icon",
    "x": 100,
    "y": 10,
    "data_key": "battery_icon"
  }
]
```

### Element Types

#### 1. Text Element (type: "text")

```json
{
  "type": "text",
  "x": X_coordinate,
  "y": Y_coordinate,
  "font": "font_name",
  "color": "color_name",
  "data_key": "data_key_name"
}
```

#### 2. Icon Element (type: "icon")

```json
{
  "type": "icon",
  "x": X_coordinate,
  "y": Y_coordinate,
  "data_key": "icon_data_key"
}
```

#### 3. Fixed Text Element (type: "fixed_text")

```json
{
  "type": "fixed_text",
  "x": X_coordinate,
  "y": Y_coordinate,
  "font": "font_name",
  "color": "color_name",
  "text": "Static text to display"
}
```

### Available Fonts

- `DejaVuSans12` - Standard font 12px
- `DejaVuSans10` - Standard font 10px
- `DejaVuSans8` - Standard font 8px
- `DejaVuSansMono12` - Monospace font 12px
- `DejaVuSansMono10` - Monospace font 10px

### Available Colors

- `white` - White
- `black` - Black
- `gray` - Gray

## 📊 Data Key Reference

### System Information

- `uptime` - System uptime
- `cpu_usage` - CPU usage percentage
- `memory_usage` - Memory usage percentage
- `temperature` - System temperature
- `load_average` - Load average

### Network Information

- `wan_ip` - WAN IP address
- `lan_ip` - LAN IP address
- `download_speed` - Download speed
- `upload_speed` - Upload speed
- `data_usage_today` - Today's data usage
- `data_usage_month` - Monthly data usage
- `ping_latency0` - First ping site latency
- `ping_latency1` - Second ping site latency

### Cellular Network Information

- `carrier_name` - Carrier name
- `signal_strength` - Signal strength
- `network_type` - Network type (4G/5G)
- `rsrp` - Reference Signal Received Power
- `rsrq` - Reference Signal Received Quality
- `sinr` - Signal-to-Interference-plus-Noise Ratio
- `band_info` - Band information

### Power Information

- `battery_percentage` - Battery percentage
- `battery_voltage` - Battery voltage
- `charging_status` - Charging status
- `power_source` - Power source type

### Icon Data Keys

- `battery_icon` - Battery icon
- `signal_icon` - Signal strength icon
- `network_type_icon` - Network type icon
- `charging_icon` - Charging status icon

### SMS Information

- `sms_count` - Unread SMS count
- `sms_list` - SMS list (recent messages)

## 💡 Configuration Examples

### Simple Single Page Configuration

```json
{
  "screen_dimmer_time_on_battery_seconds": 60,
  "screen_dimmer_time_on_dc_seconds": 86400,
  "screen_max_brightness": 100,
  "screen_min_brightness": 10,
  "template": {
    "page0": [
      {
        "type": "fixed_text",
        "x": 10,
        "y": 5,
        "font": "DejaVuSans12",
        "color": "white",
        "text": "Phonicat Status"
      },
      {
        "type": "text",
        "x": 10,
        "y": 25,
        "font": "DejaVuSans10",
        "color": "white",
        "data_key": "wan_ip"
      },
      {
        "type": "text",
        "x": 10,
        "y": 45,
        "font": "DejaVuSans10",
        "color": "white",
        "data_key": "carrier_name"
      },
      {
        "type": "icon",
        "x": 100,
        "y": 5,
        "data_key": "battery_icon"
      }
    ]
  }
}
```

### Multi-Page Configuration

```json
{
  "template": {
    "page0": [
      {
        "type": "fixed_text",
        "x": 10,
        "y": 5,
        "font": "DejaVuSans12",
        "color": "white",
        "text": "Network Status"
      },
      {
        "type": "text",
        "x": 10,
        "y": 25,
        "font": "DejaVuSansMono10",
        "color": "white",
        "data_key": "download_speed"
      },
      {
        "type": "text",
        "x": 10,
        "y": 40,
        "font": "DejaVuSansMono10",
        "color": "white",
        "data_key": "upload_speed"
      }
    ],
    "page1": [
      {
        "type": "fixed_text",
        "x": 10,
        "y": 5,
        "font": "DejaVuSans12",
        "color": "white",
        "text": "System Info"
      },
      {
        "type": "text",
        "x": 10,
        "y": 25,
        "font": "DejaVuSans10",
        "color": "white",
        "data_key": "cpu_usage"
      },
      {
        "type": "text",
        "x": 10,
        "y": 40,
        "font": "DejaVuSans10",
        "color": "white",
        "data_key": "memory_usage"
      }
    ]
  }
}
```

## 🎛️ Screen Controls

- **Short Press**: Switch to next page
- **Long Press**: Execute specific function (e.g., restart)
- **Auto-cycling**: Can be configured to automatically cycle through pages

## 🔍 Debugging Tips

1. **Use Live Preview**: The web interface provides real-time screen preview for easy debugging
2. **Add Elements Gradually**: Start with simple configurations and add complex elements step by step
3. **Check Data Keys**: Ensure correct data_key names are used
4. **Coordinate Positioning**: OLED screen resolution is 128x64 pixels, ensure elements don't exceed boundaries

## 🚨 Common Issues

### Q: Elements not displaying?
A: Check:
- Coordinates are within screen bounds (0-127, 0-63)
- data_key is correct
- Font name exists

### Q: Chinese characters showing as garbled text?
A: Save JSON file with UTF-8 encoding and use fonts that support Chinese characters

### Q: How to know which data keys are available?
A: Refer to the data key section in this document, or check the example configuration file

## 🛠️ Advanced Configuration

### Custom Page Layouts

You can create different layouts for different types of information:

```json
{
  "template": {
    "page0": [
      // Status overview page
      {
        "type": "fixed_text",
        "x": 0,
        "y": 0,
        "font": "DejaVuSans8",
        "color": "white",
        "text": "Status"
      }
    ],
    "page1": [
      // Network details page
      {
        "type": "fixed_text",
        "x": 0,
        "y": 0,
        "font": "DejaVuSans8",
        "color": "white",
        "text": "Network"
      }
    ],
    "page2": [
      // System metrics page
      {
        "type": "fixed_text",
        "x": 0,
        "y": 0,
        "font": "DejaVuSans8",
        "color": "white",
        "text": "System"
      }
    ]
  }
}
```

### Positioning Guidelines

- **Screen Size**: 128x64 pixels
- **Safe Area**: Leave 2-3 pixel margins from edges
- **Line Spacing**: 12-15 pixels between text lines for readability
- **Icon Placement**: Icons typically work well in corners or alongside text

## 📚 Reference Resources

- [GitHub Example Configuration](https://raw.githubusercontent.com/photonicat/photonicat2_mini_display/refs/heads/main/config.json)
- Web management interface screen editor
- Real-time screen preview feature

---

💡 **Tip**: Remember to click the "Restart Screen" button after modifying configuration to apply changes!