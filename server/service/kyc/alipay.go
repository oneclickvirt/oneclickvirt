package kyc

import (
	"context"
	"fmt"
	"sync"

	"oneclickvirt/global"

	"github.com/smartwalle/alipay/v3"
	"go.uber.org/zap"
)

var (
	alipayClient *alipay.Client
	alipayMu     sync.RWMutex
)

// InitAlipayClient initializes or re-initializes the Alipay client with current config.
func InitAlipayClient() error {
	cfg := global.GetAppConfig().KYC
	if cfg.AlipayAppID == "" || cfg.AlipayPrivateKey == "" {
		return fmt.Errorf("alipay config incomplete")
	}

	client, err := alipay.New(cfg.AlipayAppID, cfg.AlipayPrivateKey, true)
	if err != nil {
		return fmt.Errorf("failed to create alipay client: %v", err)
	}

	if err := client.LoadAliPayPublicKey(cfg.AlipayPublicKey); err != nil {
		return fmt.Errorf("failed to load alipay public key: %v", err)
	}

	alipayMu.Lock()
	alipayClient = client
	alipayMu.Unlock()

	global.APP_LOG.Info("alipay client initialized",
		zap.String("appID", cfg.AlipayAppID))
	return nil
}

func getAlipayClient() (*alipay.Client, error) {
	alipayMu.RLock()
	c := alipayClient
	alipayMu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("alipay client not initialized")
	}
	return c, nil
}

// AlipayFaceCertifyInit initializes a face certification session.
// Returns certify_id for the client to open the certification page.
func (s *Service) AlipayFaceCertifyInit(realName, idNumber, outerOrderNo string) (string, error) {
	client, err := getAlipayClient()
	if err != nil {
		return "", err
	}

	req := alipay.UserCertifyOpenInitialize{
		OuterOrderNo: outerOrderNo,
		BizCode:      alipay.CertifyBizCodeFace,
		IdentityParam: alipay.IdentityParam{
			IdentityType: "CERT_INFO",
			CertType:     "IDENTITY_CARD",
			CertName:     realName,
			CertNo:       idNumber,
		},
		MerchantConfig: alipay.MerchantConfig{
			ReturnURL: "", // Will be set by frontend
		},
	}

	resp, err := client.UserCertifyOpenInitialize(context.Background(), req)
	if err != nil {
		return "", fmt.Errorf("alipay certify init failed: %v", err)
	}

	if !resp.IsSuccess() {
		return "", fmt.Errorf("alipay certify init error: %s - %s", resp.SubCode, resp.SubMsg)
	}

	return resp.CertifyId, nil
}

// AlipayFaceCertifyURL generates the certification page URL.
func (s *Service) AlipayFaceCertifyURL(certifyID string) (string, error) {
	client, err := getAlipayClient()
	if err != nil {
		return "", err
	}

	req := alipay.UserCertifyOpenCertify{
		CertifyId: certifyID,
	}

	url, err := client.UserCertifyOpenCertify(req)
	if err != nil {
		return "", fmt.Errorf("alipay certify url failed: %v", err)
	}

	return url.String(), nil
}

// AlipayFaceCertifyQuery queries the certification result.
// Returns true if passed.
func (s *Service) AlipayFaceCertifyQuery(certifyID string) (bool, error) {
	client, err := getAlipayClient()
	if err != nil {
		return false, err
	}

	req := alipay.UserCertifyOpenQuery{
		CertifyId: certifyID,
	}

	resp, err := client.UserCertifyOpenQuery(context.Background(), req)
	if err != nil {
		return false, fmt.Errorf("alipay certify query failed: %v", err)
	}

	if !resp.IsSuccess() {
		return false, fmt.Errorf("alipay certify query error: %s - %s", resp.SubCode, resp.SubMsg)
	}

	return resp.Passed == "T", nil
}
