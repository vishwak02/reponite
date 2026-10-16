package query

import (
	"strings"
	"testing"
)

func cfgKey(t *testing.T, r ConfigResult, kind, key string) ConfigKey {
	t.Helper()
	for _, k := range r.Keys {
		if k.Kind == kind && k.Key == key {
			return k
		}
	}
	var have []string
	for _, k := range r.Keys {
		have = append(have, k.Kind+":"+k.Key)
	}
	t.Fatalf("no %s key %q (have %v)", kind, key, have)
	return ConfigKey{}
}

func hasSource(k ConfigKey, src string) bool {
	for _, s := range k.Sets {
		if s.Source == src {
			return true
		}
	}
	return false
}

func hasVia(k ConfigKey, via string) bool {
	for _, r := range k.Reads {
		if strings.HasPrefix(r.Via, via) {
			return true
		}
	}
	return false
}

func configFixture() dataStore {
	settings := `import os
import environ
env = environ.Env()
BASE_DIR = os.path.dirname(__file__)
DEBUG = env.bool("APP_DEBUG", default=False)
DB_HOST = os.environ.get("DB_HOST", "localhost")
SECRET = os.environ["APP_SECRET"]
STATIC_ROOT = os.path.join(BASE_DIR, "static")
ORDER_LIMIT = 50
UNUSED_KNOB = 3
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "shop.settings")
`
	views := `from django.conf import settings

def page(request):
    n = settings.ORDER_LIMIT
    m = getattr(settings, "PAGE_SIZE", 20)
    if flag_is_active(request, "new-checkout"):
        pass
`
	cli := `import argparse
import click

def main():
    p = argparse.ArgumentParser()
    p.add_argument("--max-items", type=int, default=10)
    p.add_argument("input")

@click.option("--verbose", is_flag=True, default=False, envvar="APP_VERBOSE")
def run(verbose):
    pass
`
	pyd := `from pydantic_settings import BaseSettings, SettingsConfigDict

class AppSettings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="APP_")
    port: int = 8000
    region: str
`
	ts := "const port = process.env.APP_PORT || '3000';\nconst api = import.meta.env.VITE_API_URL;\nconst { NODE_ENV, LOG_LEVEL = 'info' } = process.env;\nprocess.env.TZ = 'UTC';\nprogram.option('-w, --workers <n>', 'worker count', 4);\n"
	goSrc := "package main\n\nimport (\n\t\"flag\"\n\t\"os\"\n)\n\ntype Cfg struct {\n\tAddr string `env:\"APP_ADDR\" envDefault:\":8080\"`\n}\n\nfunc main() {\n\th := os.Getenv(\"APP_HOME\")\n\tn := flag.Int(\"retries\", 3, \"retries\")\n\tvar d string\n\tflag.StringVar(&d, \"data-dir\", \"/var/data\", \"dir\")\n\tcmd.Flags().StringP(\"mode\", \"m\", \"fast\", \"mode\")\n\tviper.SetDefault(\"server.timeout\", 30)\n\tt := viper.GetInt(\"server.timeout\")\n\t_ = h; _ = n; _ = t\n}\n"
	cpp := "void Node::init() {\n  const char* r = std::getenv(\"ROBOT_NAME\");\n  pnh.param(\"max_speed\", max_speed_, 1.0);\n  setenv(\"GST_DEBUG\", \"2\", 1);\n}\n"
	dotenv := "APP_PORT=8000\nDB_HOST=db # local\nAPP_DEBUG=true\nORPHAN_VAR=1\n"
	compose := `services:
  api:
    image: shop
    environment:
      - APP_SECRET=s3cret
      - APP_REGION
    command: ["serve", "--max-items=25"]
  worker:
    environment:
      DB_HOST: ${DB_HOST:-postgres}
      QUEUE_URL: amqp://mq
`
	k8s := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: APP_PORT
              value: "9000"
            - name: APP_SECRET
              valueFrom:
                secretKeyRef:
                  name: s
`
	docker := "FROM python:3.12\nARG BUILD_REV=dev\nENV PYTHONUNBUFFERED=1 APP_HOME=/srv\nENV LEGACY_MODE on\nRUN echo $BUILD_REV\n"
	ci := "on: push\nenv:\n  APP_DEBUG: \"false\"\njobs:\n  t:\n    env:\n      CI_ONLY: x\n    steps:\n      - run: echo ${{ env.CI_ONLY }}\n"
	launch := `<launch>
  <env name="ROSCONSOLE_FORMAT" value="[${severity}] ${message}"/>
  <node pkg="drive" type="drive_node" name="drive">
    <param name="max_speed" value="0.8"/>
    <param name="unused_param" value="1"/>
  </node>
  <arg name="robot" default="$(optenv ROBOT_NAME r1)"/>
</launch>`
	sh := "#!/bin/sh\nexport WORKERS=4\nexec gunicorn -w \"${WORKERS:-2}\" --bind \"0.0.0.0:${APP_PORT:-8000}\"\n"
	return dataStore{filesStore{files: map[string][]File{
		"shop": {
			{Path: "shop/settings.py", Content: settings},
			{Path: "shop/views.py", Content: views, Symbols: []SymbolSpan{{Name: "page", StartLine: 3, EndLine: 7}}},
			{Path: "shop/cli.py", Content: cli, Symbols: []SymbolSpan{{Name: "main", StartLine: 4, EndLine: 7}}},
			{Path: "shop/config.py", Content: pyd},
			{Path: "web/src/env.ts", Content: ts},
			{Path: "cmd/main.go", Content: goSrc, Symbols: []SymbolSpan{{Name: "main", StartLine: 12, EndLine: 22}}},
			{Path: "drive/src/node.cpp", Content: cpp, Symbols: []SymbolSpan{{Name: "init", StartLine: 1, EndLine: 5}}},
			{Path: ".env", Content: dotenv},
			{Path: "docker-compose.yml", Content: compose},
			{Path: "deploy/k8s/api.yaml", Content: k8s},
			{Path: "Dockerfile", Content: docker},
			{Path: ".github/workflows/ci.yml", Content: ci},
			{Path: "drive/launch/drive.launch", Content: launch},
			{Path: "bin/start.sh", Content: sh},
		},
	}}}
}

func TestConfigReadsAndSets(t *testing.T) {
	s := configFixture()
	r := ConfigResult{Keys: collectConfig(s, FleetRepo, "v1")}
	full := Config(s, FleetRepo, "v1", "", ConfigOptions{Budget: 100000})
	// env read with a default in code, set in .env, k8s and compose.
	port := cfgKey(t, r, CfgEnv, "APP_PORT")
	if !hasVia(port, "process.env") || !hasSource(port, "dotenv") || !hasSource(port, "k8s") || port.Status != "" {
		t.Errorf("APP_PORT: %+v", port)
	}
	if !containsStr(port.Defaults, "'3000'") || !containsStr(port.Defaults, "8000") {
		t.Errorf("APP_PORT defaults (js || and ${:-}) %v", port.Defaults)
	}
	sec := cfgKey(t, r, CfgEnv, "APP_SECRET")
	if !hasSource(sec, "compose") || !hasSource(sec, "k8s") {
		t.Errorf("APP_SECRET sets: %+v", sec.Sets)
	}
	for _, x := range sec.Sets {
		if x.Source == "k8s" && x.Value != "(from secretKeyRef)" {
			t.Errorf("k8s valueFrom: %+v", x)
		}
	}
	db := cfgKey(t, r, CfgEnv, "DB_HOST")
	if !containsStr(db.Defaults, `"localhost"`) || !hasSource(db, "dotenv") || !hasVia(db, "compose interpolation") {
		t.Errorf("DB_HOST: %+v", db)
	}
	for _, x := range db.Sets {
		if x.Source == "dotenv" && x.Value != "db" {
			t.Errorf("dotenv inline comment stripped: %q", x.Value)
		}
	}
	if k := cfgKey(t, r, CfgEnv, "APP_DEBUG"); !hasVia(k, "django-environ") || !hasSource(k, "ci") || k.Defaults[0] != "False" {
		t.Errorf("APP_DEBUG: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "APP_REGION"); !hasVia(k, "pydantic-settings") || !hasSource(k, "compose") {
		t.Errorf("pydantic field with env_prefix: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "APP_VERBOSE"); !hasVia(k, "click envvar") {
		t.Errorf("click envvar: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "APP_ADDR"); !k.Reads[0].HasDefault || k.Reads[0].Default != ":8080" {
		t.Errorf("go env tag default: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "APP_HOME"); !hasSource(k, "dockerfile ENV") || k.Reads[0].Handle != "cmd.main" {
		t.Errorf("os.Getenv + Dockerfile ENV, handle: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "LEGACY_MODE"); k.Sets[0].Value != "on" || k.Status != "set_never_read" {
		t.Errorf("ENV k v form: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "BUILD_REV"); k.Status != "" || !hasVia(k, "shell expansion") {
		t.Errorf("ARG read by the Dockerfile itself: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "ROBOT_NAME"); !hasVia(k, "getenv") || !hasVia(k, "$(optenv)") {
		t.Errorf("C++ getenv + launch optenv: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "GST_DEBUG"); !hasSource(k, "code") {
		t.Errorf("setenv: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "ROSCONSOLE_FORMAT"); !hasSource(k, "launch") {
		t.Errorf("launch <env>: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "WORKERS"); !hasSource(k, "shell") || !hasVia(k, "${") {
		t.Errorf("shell export + expansion: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "CI_ONLY"); !hasVia(k, "ci expression") {
		t.Errorf("ci expression read: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "LOG_LEVEL"); k.Defaults[0] != "'info'" {
		t.Errorf("destructured default: %+v", k)
	}
	// Status flags.
	if k := cfgKey(t, r, CfgEnv, "ORPHAN_VAR"); k.Status != "set_never_read" {
		t.Errorf("ORPHAN_VAR: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "QUEUE_URL"); k.Status != "set_never_read" {
		t.Errorf("QUEUE_URL: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "VITE_API_URL"); k.Status != "read_never_set" {
		t.Errorf("VITE_API_URL: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "PYTHONUNBUFFERED"); k.Status != "" {
		t.Errorf("runtime env is not an orphan: %+v", k)
	}
	if k := cfgKey(t, r, CfgEnv, "DJANGO_SETTINGS_MODULE"); !hasSource(k, "code") {
		t.Errorf("environ.setdefault is a set: %+v", k)
	}
	// Settings.
	if k := cfgKey(t, r, CfgSetting, "ORDER_LIMIT"); !hasSource(k, "settings") || k.Status != "" || k.Reads[0].Handle != "shop.page" {
		t.Errorf("ORDER_LIMIT: %+v", k)
	}
	if k := cfgKey(t, r, CfgSetting, "PAGE_SIZE"); k.Status != "read_never_set" || k.Defaults[0] != "20" {
		t.Errorf("PAGE_SIZE: %+v", k)
	}
	if k := cfgKey(t, r, CfgSetting, "UNUSED_KNOB"); k.Status != "set_never_read" {
		t.Errorf("UNUSED_KNOB: %+v", k)
	}
	if k := cfgKey(t, r, CfgSetting, "BASE_DIR"); k.Status != "" {
		t.Errorf("a setting used inside the settings module is read: %+v", k)
	}
	if k := cfgKey(t, r, CfgSetting, "STATIC_ROOT"); k.Status != "" {
		t.Errorf("framework setting is not an orphan: %+v", k)
	}
	if k := cfgKey(t, r, CfgSetting, "server.timeout"); !k.Reads[0].HasDefault || k.Reads[0].Default != "30" {
		t.Errorf("viper default: %+v", k)
	}
	// Flags.
	if k := cfgKey(t, r, CfgFlag, "--max-items"); k.Defaults[0] != "10" || len(k.Sets) != 1 || k.Sets[0].Value != "25" || k.Sets[0].Source != "compose" {
		t.Errorf("argparse flag passed in compose: %+v", k)
	}
	cfgKey(t, r, CfgFlag, "input")
	for _, want := range [][2]string{{"retries", "3"}, {"data-dir", `"/var/data"`}, {"mode", `"fast"`}, {"--workers", "4"}} {
		if k := cfgKey(t, r, CfgFlag, want[0]); k.Defaults[0] != want[1] {
			t.Errorf("flag %s default %v, want %s", want[0], k.Defaults, want[1])
		}
	}
	// Feature flags and ROS params.
	cfgKey(t, r, CfgFeature, "new-checkout")
	if k := cfgKey(t, r, CfgROSParam, "max_speed"); k.Reads[0].Default != "" || len(k.Sets) != 1 || k.Sets[0].Value != "0.8" {
		t.Errorf("ros param read + launch set: %+v", k)
	}
	if k := cfgKey(t, r, CfgROSParam, "unused_param"); k.Status != "set_never_read" {
		t.Errorf("unused launch param: %+v", k)
	}
	if len(full.ReadNeverSet) == 0 || len(full.SetNeverRead) == 0 {
		t.Errorf("flag lists empty: %d %d", len(full.ReadNeverSet), len(full.SetNeverRead))
	}
}

func TestConfigDetailAndBudget(t *testing.T) {
	s := configFixture()
	d := Config(s, FleetRepo, "v1", "app_port", ConfigOptions{})
	if len(d.Keys) != 1 || d.Keys[0].Key != "APP_PORT" || len(d.Keys[0].Reads) < 2 || len(d.Keys[0].Sets) < 2 {
		t.Fatalf("detail by case-insensitive env name: %+v", d.Keys)
	}
	if k := Config(s, FleetRepo, "v1", "max_items", ConfigOptions{}); len(k.Keys) != 1 || k.Keys[0].Key != "--max-items" {
		t.Errorf("flag spelled with underscores: %+v", k.Keys)
	}
	if miss := Config(s, FleetRepo, "v1", "APP_PRT", ConfigOptions{}); len(miss.Keys) != 0 || !strings.Contains(miss.Note, "no configuration key") {
		t.Errorf("miss: %+v", miss)
	}
	tiny := Config(s, FleetRepo, "v1", "", ConfigOptions{Budget: 200})
	if !tiny.Truncated || tiny.Omitted == 0 || !strings.Contains(tiny.Next, "--budget") {
		t.Errorf("list budget: truncated=%v omitted=%d next=%q", tiny.Truncated, tiny.Omitted, tiny.Next)
	}
	envOnly := Config(s, FleetRepo, "v1", "", ConfigOptions{Budget: 100000, Kind: CfgFlag})
	for _, k := range envOnly.Keys {
		if k.Kind != CfgFlag {
			t.Errorf("kind filter leaked %s", k.Kind)
		}
	}
}

func TestConfigDynamicKeyNotFlagged(t *testing.T) {
	s := dataStore{filesStore{files: map[string][]File{"a": {{Path: "a/boot.py", Content: "import os\nfor i in range(3):\n    t = os.environ.get(f\"TOKEN_{i}\")\n"}}}}}
	keys := collectConfig(s, FleetRepo, "v1")
	if len(keys) != 1 || keys[0].Key != "TOKEN_*" || keys[0].Status != "" {
		t.Errorf("dynamic key: %+v", keys)
	}
}

// A Go key read through a named string constant resolves to the constant's
// value, across files of the same package; an identifier that is not a
// constant is not guessed.
func TestConfigGoEnvThroughConstant(t *testing.T) {
	s := dataStore{filesStore{files: map[string][]File{"a": {
		{Path: "cmd/app/env.go", Content: "package main\n\nconst storeEnv = \"APP_STORE_DIR\"\n\nconst (\n\trefsEnv string = \"APP_REFS\" // pins\n)\n"},
		{Path: "cmd/app/main.go", Content: "package main\n\nimport \"os\"\n\nfunc main() {\n\t_ = os.Getenv(storeEnv)\n\t_, _ = os.LookupEnv(refsEnv)\n\t_ = os.Getenv(dynamic)\n}\n"},
	}}}}
	got := map[string]bool{}
	for _, k := range collectConfig(s, FleetRepo, "v1") {
		got[k.Key] = true
	}
	if !got["APP_STORE_DIR"] || !got["APP_REFS"] {
		t.Errorf("constant-named env keys not resolved: %v", got)
	}
	if got["dynamic"] {
		t.Errorf("a non-constant identifier must not become a key: %v", got)
	}
}
