// Copyright 2025 qc <2192629378@qq.com>. All Rights Reserved.
// Use of this source code is governed by a MIT style
// License that can be found in the LICENSE file.
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ════════════════════════════════════════════════════════════════════════════
// stringSliceFlag (支持多次出现的 flag)
// ════════════════════════════════════════════════════════════════════════════

func TestStringSliceFlag_Set(t *testing.T) {
	var s stringSliceFlag
	require.NoError(t, s.Set("a=1"))
	require.NoError(t, s.Set("b=2"))
	require.NoError(t, s.Set("c=3"))
	assert.Equal(t, []string{"a=1", "b=2", "c=3"}, []string(s))
}

func TestStringSliceFlag_String(t *testing.T) {
	s := stringSliceFlag{"a=1", "b=2"}
	assert.Equal(t, "a=1,b=2", s.String())
}

func TestStringSliceFlag_Empty(t *testing.T) {
	var s stringSliceFlag
	assert.Equal(t, "", s.String())
}

// ════════════════════════════════════════════════════════════════════════════
// parseKVPairs
// ════════════════════════════════════════════════════════════════════════════

func TestParseKVPairs_Basic(t *testing.T) {
	got, err := parseKVPairs([]string{"password=xxx", "user=admin"}, "from-literal")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"password": "xxx",
		"user":     "admin",
	}, got)
}

func TestParseKVPairs_Empty(t *testing.T) {
	got, err := parseKVPairs(nil, "from-literal")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestParseKVPairs_MissingEquals(t *testing.T) {
	_, err := parseKVPairs([]string{"justakey"}, "from-literal")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少 = 号")
	assert.Contains(t, err.Error(), "from-literal")
}

func TestParseKVPairs_EmptyKey(t *testing.T) {
	_, err := parseKVPairs([]string{"=value"}, "from-literal")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key 不能为空")
}

func TestParseKVPairs_ValueWithEquals(t *testing.T) {
	// value 里有 = 号应保留 (例: base64 编码)
	got, err := parseKVPairs([]string{"token=abc==def="}, "from-literal")
	require.NoError(t, err)
	assert.Equal(t, "abc==def=", got["token"])
}

func TestParseKVPairs_TrimsKeyOnly(t *testing.T) {
	// key 前后空格 trim, value 保留
	got, err := parseKVPairs([]string{"  password = xxx "}, "from-literal")
	require.NoError(t, err)

	// "  password " trim 后 = "password"
	assert.Equal(t, " xxx ", got["password"])
}

func TestParseKVPairs_DuplicateKeyLastWins(t *testing.T) {
	// 跟 kubectl 行为一致: 重复 key 后者覆盖
	got, err := parseKVPairs([]string{
		"password=first",
		"password=second",
	}, "from-literal")
	require.NoError(t, err)
	assert.Equal(t, "second", got["password"])
}

func TestParseKVPairs_EmptyValue(t *testing.T) {
	// "key=" 有 = 号但 value 为空, 应允许 (Empty value 是合法 Secret 值)
	got, err := parseKVPairs([]string{"key="}, "from-literal")
	require.NoError(t, err)
	assert.Equal(t, "", got["key"])
}
