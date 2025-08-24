# Unit Tests for Photonicat2 Display

This directory contains comprehensive unit tests for the photonicat2 display Go application.

## Test Files

### Core Tests
- **`test_utils_test.go`** - Tests for utility functions (frame buffers, color handling, system utilities)
- **`test_processData_test.go`** - Tests for data processing functions (network, system info, security)
- **`test_draw_test.go`** - Tests for graphics and drawing functions (text, images, shapes)
- **`test_main_test.go`** - Tests for core data structures and initialization functions

### Feature-Specific Tests  
- **`test_processSms_test.go`** - Tests for SMS handling and text processing
- **`test_httpServer_test.go`** - Tests for web server security and configuration
- **`test_powerGraph_test.go`** - Tests for power monitoring and graph visualization

## Running Tests

### Run All Tests
```bash
# From the main project directory
go test -v ./tests/

# Or run all tests recursively
go test -v ./...
```

### Run Specific Test Files
```bash
# Run utils tests
go test -v ./tests/ -run TestClearFrame

# Run security tests
go test -v ./tests/ -run TestValidateJSON

# Run drawing tests  
go test -v ./tests/ -run TestDrawText
```

### Run Tests by Category
```bash
# Graphics/drawing tests
go test -v ./tests/ -run "Test.*Draw|Test.*Copy|Test.*Image"

# Security tests
go test -v ./tests/ -run "Test.*Valid|Test.*Secure|Test.*Sanitize"

# Text processing tests
go test -v ./tests/ -run "Test.*Text|Test.*Wrap|Test.*CJK"
```

### Use the Test Runner Script
```bash
# From the main project directory
./run_tests.sh
```

## Test Coverage

### Security Functions ✅
- JSON validation and sanitization (`TestValidateJSON`, `TestSecureUnmarshal`)
- Command argument sanitization (`TestSanitizeCommandArg`)
- Input validation and bounds checking
- Memory safety in image operations

### Graphics Functions ✅
- Text rendering with font support (`TestDrawText`)
- Image composition and manipulation (`TestCopyImageToImageAt`)
- Color space conversions (`TestBlendColors`, `TestHsvToRgb`)
- Drawing primitives (`TestDrawRect`, `TestDrawLine`)

### System Functions ✅
- Frame buffer management (`TestGetFrameBuffer`, `TestReturnFrameBuffer`)
- Network data processing (`TestFormatSpeed`, `TestGetNetworkSpeed`)
- System information gathering (`TestGetUptime`, `TestGetFanSpeed`)
- Configuration management (`TestDeepMerge`, `TestDeepCopy`)

### SMS/Text Functions ✅
- Text wrapping with CJK support (`TestWrapText`)
- SMS data parsing and display (`TestCollectAndDrawSms`)
- Unicode character handling (`TestIsCJK`)

### Power Management ✅
- Power graph visualization (`TestDrawPowerGraph`)
- Data collection and storage (`TestRecordPowerSample`)
- Color blending for graphs (`TestBlendColors`)

## Test Statistics
- **Total Test Files**: 7
- **Functions Tested**: 40+ critical functions  
- **Individual Test Cases**: 150+ test scenarios
- **Coverage Areas**: Security, Graphics, Data Processing, Text Handling, System Management

## File Organization
Tests are organized with descriptive prefixes:
- `test_*_test.go` - Organized by source file they test
- All tests are in the main package to access unexported functions
- Tests can be run individually or as a complete suite

## Notes
- Tests are designed to handle missing system files/hardware gracefully
- Some tests may show expected failures in test environments without actual hardware
- Security tests focus on preventing injection attacks and validating inputs
- Graphics tests verify correct image manipulation and rendering logic
- Run `./run_tests.sh` for automated testing with categorized results and coverage reports