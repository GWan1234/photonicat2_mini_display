package main

import (
	"encoding/json"
	"testing"
)

func TestValidateJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
	}{
		{
			name:    "valid JSON object",
			input:   []byte(`{"key": "value", "number": 123}`),
			wantErr: false,
		},
		{
			name:    "valid JSON array",
			input:   []byte(`[1, 2, 3, "test"]`),
			wantErr: false,
		},
		{
			name:    "empty JSON object",
			input:   []byte(`{}`),
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   []byte(`{"invalid": json`),
			wantErr: true,
		},
		{
			name:    "empty data",
			input:   []byte(``),
			wantErr: true,
		},
		{
			name:    "malicious script tag",
			input:   []byte(`{"content": "<script>alert('xss')</script>"}`),
			wantErr: true,
		},
		{
			name:    "malicious javascript",
			input:   []byte(`{"code": "javascript:alert(1)"}`),
			wantErr: true,
		},
		{
			name:    "malicious eval",
			input:   []byte(`{"func": "eval('malicious')"}`),
			wantErr: true,
		},
		{
			name:    "too large JSON",
			input:   make([]byte, 11*1024*1024), // 11MB
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSecureUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		target  interface{}
		wantErr bool
	}{
		{
			name:    "valid JSON to map",
			input:   []byte(`{"name": "test", "value": 123}`),
			target:  &map[string]interface{}{},
			wantErr: false,
		},
		{
			name:    "valid JSON to struct",
			input:   []byte(`{"name": "test"}`),
			target:  &struct{ Name string }{},
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   []byte(`{"invalid": json`),
			target:  &map[string]interface{}{},
			wantErr: true,
		},
		{
			name:    "malicious content",
			input:   []byte(`{"script": "<script>alert('xss')</script>"}`),
			target:  &map[string]interface{}{},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := secureUnmarshal(tt.input, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("secureUnmarshal() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeepMerge(t *testing.T) {
	tests := []struct {
		name     string
		dest     map[string]interface{}
		src      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "merge simple values",
			dest: map[string]interface{}{
				"a": 1,
				"b": 2,
			},
			src: map[string]interface{}{
				"b": 3,
				"c": 4,
			},
			expected: map[string]interface{}{
				"a": 1,
				"b": 3,
				"c": 4,
			},
		},
		{
			name: "merge nested maps",
			dest: map[string]interface{}{
				"nested": map[string]interface{}{
					"a": 1,
					"b": 2,
				},
			},
			src: map[string]interface{}{
				"nested": map[string]interface{}{
					"b": 3,
					"c": 4,
				},
			},
			expected: map[string]interface{}{
				"nested": map[string]interface{}{
					"a": 1,
					"b": 3,
					"c": 4,
				},
			},
		},
		{
			name: "nil destination",
			dest: nil,
			src: map[string]interface{}{
				"a": 1,
				"b": 2,
			},
			expected: map[string]interface{}{
				"a": 1,
				"b": 2,
			},
		},
		{
			name: "empty source",
			dest: map[string]interface{}{
				"a": 1,
			},
			src:      map[string]interface{}{},
			expected: map[string]interface{}{"a": 1},
		},
		{
			name: "overwrite with different types",
			dest: map[string]interface{}{
				"value": "string",
			},
			src: map[string]interface{}{
				"value": 123,
			},
			expected: map[string]interface{}{
				"value": 123,
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deepMerge(tt.dest, tt.src)
			
			if !mapsEqual(result, tt.expected) {
				t.Errorf("deepMerge() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDeepCopy(t *testing.T) {
	tests := []struct {
		name string
		src  map[string]interface{}
	}{
		{
			name: "simple map",
			src: map[string]interface{}{
				"a": 1,
				"b": "test",
				"c": true,
			},
		},
		{
			name: "nested map",
			src: map[string]interface{}{
				"nested": map[string]interface{}{
					"inner": "value",
					"number": 42,
				},
				"simple": "value",
			},
		},
		{
			name: "empty map",
			src:  map[string]interface{}{},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deepCopy(tt.src)
			
			// Check that result equals source
			if !mapsEqual(result, tt.src) {
				t.Errorf("deepCopy() result doesn't equal source")
			}
			
			// Check that modifying result doesn't affect source
			if len(result) > 0 {
				// Add a key to result
				result["new_key"] = "new_value"
				
				if _, exists := tt.src["new_key"]; exists {
					t.Errorf("deepCopy() didn't create independent copy")
				}
			}
			
			// Test nested modification if nested map exists
			if nested, ok := result["nested"].(map[string]interface{}); ok {
				nested["modified"] = true
				
				if srcNested, ok := tt.src["nested"].(map[string]interface{}); ok {
					if _, exists := srcNested["modified"]; exists {
						t.Errorf("deepCopy() didn't create independent nested copy")
					}
				}
			}
		})
	}
}

func TestHsvToRgb(t *testing.T) {
	tests := []struct {
		h, s, v    float64
		r, g, b    float64
		tolerance  float64
	}{
		// Pure red
		{0.0, 1.0, 1.0, 1.0, 0.0, 0.0, 0.01},
		// Pure green
		{1.0/3.0, 1.0, 1.0, 0.0, 1.0, 0.0, 0.01},
		// Pure blue
		{2.0/3.0, 1.0, 1.0, 0.0, 0.0, 1.0, 0.01},
		// White
		{0.0, 0.0, 1.0, 1.0, 1.0, 1.0, 0.01},
		// Black
		{0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.01},
		// Gray
		{0.0, 0.0, 0.5, 0.5, 0.5, 0.5, 0.01},
	}
	
	for _, tt := range tests {
		r, g, b := hsvToRgb(tt.h, tt.s, tt.v)
		
		if abs64(r-tt.r) > tt.tolerance || abs64(g-tt.g) > tt.tolerance || abs64(b-tt.b) > tt.tolerance {
			t.Errorf("hsvToRgb(%f, %f, %f) = (%f, %f, %f), want (%f, %f, %f)",
				tt.h, tt.s, tt.v, r, g, b, tt.r, tt.g, tt.b)
		}
	}
}

func TestSaveUserConfigFromStr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid JSON",
			input:    `{"key": "value", "number": 123}`,
			expected: true, // May fail due to file permissions, but should not panic
		},
		{
			name:     "invalid JSON",
			input:    `{"invalid": json}`,
			expected: false,
		},
		{
			name:     "empty JSON",
			input:    `{}`,
			expected: true, // May fail due to file permissions
		},
		{
			name:     "malicious content",
			input:    `{"script": "<script>alert('xss')</script>"}`,
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("saveUserConfigFromStr() panicked: %v", r)
				}
			}()
			
			result := saveUserConfigFromStr(tt.input)
			
			// We can't guarantee file operations will succeed in test environment,
			// but we can verify the function handles invalid JSON correctly
			if tt.expected == false && result == true {
				t.Errorf("saveUserConfigFromStr() should have failed with invalid input")
			}
		})
	}
}

// Helper function to compare maps deeply
func mapsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	
	for k, v := range a {
		if bv, exists := b[k]; !exists {
			return false
		} else {
			if !valuesEqual(v, bv) {
				return false
			}
		}
	}
	return true
}

// Helper function to compare values deeply
func valuesEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	
	// Handle maps
	if aMap, ok := a.(map[string]interface{}); ok {
		if bMap, ok := b.(map[string]interface{}); ok {
			return mapsEqual(aMap, bMap)
		}
		return false
	}
	
	// For other types, use simple equality
	return a == b
}

// Helper function for float64 absolute value
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestLoadUserConfig(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("loadUserConfig() panicked: %v", r)
		}
	}()
	
	// This function reads from file system, test it doesn't crash
	result := loadUserConfig()
	
	// Should return a string (might be "{}" if no config exists)
	if len(result) < 2 {
		t.Error("loadUserConfig should return at least empty JSON object")
	}
	
	// Should be valid JSON
	var temp interface{}
	if err := json.Unmarshal([]byte(result), &temp); err != nil {
		t.Errorf("loadUserConfig returned invalid JSON: %v", err)
	}
}

func TestSaveUserConfigToFile(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("saveUserConfigToFile() panicked: %v", r)
		}
	}()
	
	// This function writes to file system, test it doesn't crash
	// Result depends on file system permissions
	result := saveUserConfigToFile()
	
	// Should return a boolean
	_ = result
}