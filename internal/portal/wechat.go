package portal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ExchangeOpenID code 换 openId（小程序 wx.login 拿 code，后端凭 appid+secret 换）。
// 标准库直调 jscode2session，不引入新依赖。
func ExchangeOpenID(appid, secret, code string) (string, error) {
	if appid == "" || secret == "" {
		return "", errors.New("未配置微信小程序")
	}
	if code == "" {
		return "", errors.New("缺少微信 code")
	}
	url := fmt.Sprintf("https://api.weixin.qq.com/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		appid, secret, code)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.OpenID == "" {
		return "", fmt.Errorf("微信登录失败: %s", out.ErrMsg)
	}
	return out.OpenID, nil
}
