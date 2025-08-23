# Photonicat 屏幕配置编程指南

本指南将帮助您了解如何自定义 Photonicat 设备的 OLED 屏幕显示内容。

## 📋 概述

Photonicat 设备支持通过 JSON 配置文件完全自定义屏幕显示内容。您可以配置多个页面，每个页面包含不同的元素，如文本、图标和固定文本。

## 🔧 基本配置结构

### 顶级配置项

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

**配置说明：**

- `screen_dimmer_time_on_battery_seconds`: 电池模式下自动变暗时间（秒）
- `screen_dimmer_time_on_dc_seconds`: 充电模式下自动变暗时间（秒）
- `screen_max_brightness`: 最大亮度 (0-100)
- `screen_min_brightness`: 最小亮度 (0-100)
- `ping_site0`, `ping_site1`: 用于网络延迟测试的网站
- `show_sms`: 是否显示短信功能
- `sms_limit_for_screen`: 屏幕上显示的短信数量限制
- `template`: 屏幕页面模板配置

## 📄 页面和元素配置

### 页面结构

每个页面 (`page0`, `page1`, etc.) 包含一个元素数组：

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

### 元素类型

#### 1. 文本元素 (type: "text")

```json
{
  "type": "text",
  "x": 坐标X,
  "y": 坐标Y,
  "font": "字体名称",
  "color": "颜色",
  "data_key": "数据键"
}
```

#### 2. 图标元素 (type: "icon")

```json
{
  "type": "icon",
  "x": 坐标X,
  "y": 坐标Y,
  "data_key": "图标数据键"
}
```

#### 3. 固定文本元素 (type: "fixed_text")

```json
{
  "type": "fixed_text",
  "x": 坐标X,
  "y": 坐标Y,
  "font": "字体名称",
  "color": "颜色",
  "text": "固定显示的文本"
}
```

### 可用字体

- `DejaVuSans12` - 标准字体 12px
- `DejaVuSans10` - 标准字体 10px
- `DejaVuSans8` - 标准字体 8px
- `DejaVuSansMono12` - 等宽字体 12px
- `DejaVuSansMono10` - 等宽字体 10px

### 可用颜色

- `white` - 白色
- `black` - 黑色
- `gray` - 灰色

## 📊 数据键 (data_key) 参考

### 系统信息

- `uptime` - 系统运行时间
- `cpu_usage` - CPU 使用率
- `memory_usage` - 内存使用率
- `temperature` - 系统温度
- `load_average` - 负载平均值

### 网络信息

- `wan_ip` - WAN IP 地址
- `lan_ip` - LAN IP 地址
- `download_speed` - 下载速度
- `upload_speed` - 上传速度
- `data_usage_today` - 今日数据用量
- `data_usage_month` - 本月数据用量
- `ping_latency0` - 第一个 ping 站点延迟
- `ping_latency1` - 第二个 ping 站点延迟

### 蜂窝网络信息

- `carrier_name` - 运营商名称
- `signal_strength` - 信号强度
- `network_type` - 网络类型 (4G/5G)
- `rsrp` - 参考信号接收功率
- `rsrq` - 参考信号接收质量
- `sinr` - 信噪比
- `band_info` - 频段信息

### 电源信息

- `battery_percentage` - 电池百分比
- `battery_voltage` - 电池电压
- `charging_status` - 充电状态
- `power_source` - 电源类型

### 图标数据键

- `battery_icon` - 电池图标
- `signal_icon` - 信号图标
- `network_type_icon` - 网络类型图标
- `charging_icon` - 充电图标

### 短信信息

- `sms_count` - 未读短信数量
- `sms_list` - 短信列表（最近的几条）

## 💡 配置示例

### 简单的单页配置

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
        "text": "Photonicat Status"
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

### 多页配置

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

## 🎛️ 屏幕操作

- **短按按钮**: 切换到下一页
- **长按按钮**: 执行特定功能（如重启）
- **自动切换**: 可配置自动在页面间循环

## 🔍 调试技巧

1. **使用实时预览**: Web 界面提供实时屏幕预览，方便调试
2. **逐步添加元素**: 从简单配置开始，逐步添加复杂元素
3. **检查数据键**: 确保使用正确的 data_key 名称
4. **坐标定位**: OLED 屏幕分辨率为 128x64 像素，注意元素不要超出边界

## 🚨 常见问题

### Q: 元素不显示怎么办？
A: 检查：
- 坐标是否在屏幕范围内 (0-127, 0-63)
- data_key 是否正确
- 字体名称是否存在

### Q: 中文显示乱码怎么办？
A: 使用 UTF-8 编码保存 JSON 文件，确保使用支持中文的字体

### Q: 如何知道有哪些数据键可用？
A: 参考本文档的数据键部分，或查看示例配置文件

## 📚 参考资源

- [GitHub 示例配置](https://raw.githubusercontent.com/photonicat/photonicat2_mini_display/refs/heads/main/config.json)
- Web 管理界面的屏幕编辑器
- 实时屏幕预览功能

---

💡 **提示**: 修改配置后记得点击"重启屏幕"按钮使更改生效！