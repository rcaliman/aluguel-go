package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address               string         `json:"address"`
	Database              DatabaseConfig `json:"database"`
	SecureCookies         bool           `json:"secure_cookies"`
	SessionHours          int            `json:"session_hours"`
	SessionTimeoutSeconds int            `json:"session_timeout_seconds,omitempty"`
}
type DatabaseConfig struct {
	Host       string            `json:"host"`
	Port       string            `json:"port"`
	Name       string            `json:"name"`
	Username   string            `json:"username"`
	Password   string            `json:"password"`
	Timezone   string            `json:"timezone"`
	Parameters map[string]string `json:"parameters"`
	MaxOpen    int               `json:"max_open_connections"`
	MaxIdle    int               `json:"max_idle_connections"`
}

func LoadConfig(path string) (Config, error) {
	c := Config{Address: ":8080", Database: DatabaseConfig{Host: "localhost", Port: "3306", Timezone: "America/Sao_Paulo", MaxOpen: 10, MaxIdle: 5}}
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	e = decoder.Decode(&c)
	if e != nil {
		return c, e
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		return c, fmt.Errorf("configuração deve conter apenas um objeto JSON")
	}
	if _, _, e = net.SplitHostPort(c.Address); e != nil {
		return c, fmt.Errorf("address inválido: %w", e)
	}
	port, err := strconv.Atoi(c.Database.Port)
	if err != nil || port < 1 || port > 65535 {
		return c, fmt.Errorf("database.port inválida")
	}
	if c.Database.MaxOpen < 1 || c.Database.MaxIdle < 0 || c.Database.MaxIdle > c.Database.MaxOpen {
		return c, fmt.Errorf("limites de conexão inválidos")
	}
	if c.Database.Name == "" {
		return c, fmt.Errorf("database.name é obrigatório")
	}
	if c.SessionHours < 0 || c.SessionTimeoutSeconds < 0 {
		return c, fmt.Errorf("tempo de sessão inválido")
	}
	return c, nil
}

func (c Config) sessionTimeout() time.Duration {
	if c.SessionTimeoutSeconds > 0 {
		return time.Duration(c.SessionTimeoutSeconds) * time.Second
	}
	// Compatibility with JSON files produced by the first converter.
	if c.SessionHours > 0 {
		return time.Duration(c.SessionHours) * time.Hour
	}
	return 30 * time.Minute
}
func (c DatabaseConfig) DSN() (string, error) {
	_, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return "", e
	}
	m := mysql.NewConfig()
	m.User = c.Username
	m.Passwd = c.Password
	m.Net = "tcp"
	m.Addr = net.JoinHostPort(strings.Trim(c.Host, "[]"), c.Port)
	m.DBName = c.Name
	m.ParseTime = true
	// Audit timestamps are stored and interpreted in UTC.
	// DATE fields remain civil dates; Timezone is used for presentation only.
	m.Loc = time.UTC
	m.Timeout = 10 * time.Second
	m.ReadTimeout = 20 * time.Second
	m.WriteTimeout = 20 * time.Second
	m.Params = map[string]string{}
	for k, v := range c.Parameters {
		switch k {
		case "tls":
			m.TLSConfig = v
		case "allowNativePasswords":
			m.AllowNativePasswords = v == "true"
		case "allowCleartextPasswords":
			m.AllowCleartextPasswords = v == "true"
		case "charset", "collation":
			m.Params[k] = v
		default:
			return "", fmt.Errorf("parâmetro MySQL não suportado: %s", k)
		}
	}
	return m.FormatDSN(), nil
}
