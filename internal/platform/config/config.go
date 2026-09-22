package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Server  ServerConfig  `toml:"server"`
	Mongo   MongoConfig   `toml:"mongo"`
	Session SessionConfig `toml:"session"`
	Storage StorageConfig `toml:"storage"`
	Log     LogConfig     `toml:"log"`
	Seed    SeedConfig    `toml:"seed"`
}

type ServerConfig struct {
	Addr string `toml:"addr"`
}

type MongoConfig struct {
	URI      string `toml:"uri"`
	Database string `toml:"database"`
}

type SessionConfig struct {
	TTLHours      int    `toml:"ttl_hours"`
	CookieName    string `toml:"cookie_name"`
	SysCookieName string `toml:"sys_cookie_name"`
	Secret        string `toml:"secret"`
	Secure        bool   `toml:"secure"` // HTTPS 部署时置 true，cookie 加 Secure 标记
}

type StorageConfig struct {
	UploadDir string `toml:"upload_dir"`
}

type LogConfig struct {
	Level string `toml:"level"`
}

type SeedConfig struct {
	Enabled       bool   `toml:"enabled"`
	SysadminUser  string `toml:"sysadmin_user"`
	SysadminPass  string `toml:"sysadmin_pass"`
	DemoTenant    bool   `toml:"demo_tenant"`
	DemoAdminUser string `toml:"demo_admin_user"`
	DemoAdminPass string `toml:"demo_admin_pass"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Session.TTLHours <= 0 {
		c.Session.TTLHours = 12
	}
	if c.Session.CookieName == "" {
		c.Session.CookieName = "erp_sid"
	}
	if c.Session.SysCookieName == "" {
		c.Session.SysCookieName = "erp_sys"
	}
	if c.Storage.UploadDir == "" {
		c.Storage.UploadDir = "./data/uploads"
	}
	return &c, nil
}
