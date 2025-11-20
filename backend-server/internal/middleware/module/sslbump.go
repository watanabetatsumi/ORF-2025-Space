package module

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"sync"
	"time"
)

// TODO: Squidのようにクライアントから受け取ったHTTPSリクエストを、証明書を偽証してBumpする実装を追加
// TODO: 暗号化されたユーザーからの処理を読み込む
// TODO: HTTPヘッダーからドメインを抽出
// TODO: /etc/squid/bump.crt(ユーザーに信頼させてるルート証明書)に対して、bump.key(秘密鍵)を使って、ドメインごとの偽証明書を動的に生成
// TODO: 生成した偽証明書を使って、クライアントとサーバーの間で中間者攻撃を実施
// TODO: SSL/TLS通信を復号化して、平文のHTTPリクエストデータを取得
// TODO: 取得したHTTPリクエストデータをmodel.BpRequest構造体に変換して返す

type SSLBumpHandler struct {
	crtPath   string
	keyPath   string
	caCert    *x509.Certificate
	caKey     any
	sharedKey *rsa.PrivateKey // 追加: 使い回すための共通秘密鍵

	// 証明書キャッシュ（オンメモリ）
	certCache     map[string]*tls.Certificate
	certCacheKeys []string // キャッシュの順序管理用（FIFO）
	cacheMu       sync.RWMutex
	maxCacheSize  int
}

func NewSSLBumpHandler(crtPath, keyPath string, maxCacheSize int) (*SSLBumpHandler, error) {
	h := &SSLBumpHandler{
		crtPath:       crtPath,
		keyPath:       keyPath,
		certCache:     make(map[string]*tls.Certificate),
		certCacheKeys: make([]string, 0, maxCacheSize),
		maxCacheSize:  maxCacheSize,
	}

	// 追加: 高速化のために、サーバー証明書用の共通RSA鍵を事前に生成しておく
	var err error
	h.sharedKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate shared server key: %w", err)
	}

	if err := h.loadCA(); err != nil {
		return nil, err
	}
	return h, nil
}

func (s *SSLBumpHandler) loadCA() error {
	// CA証明書を読み込む
	certPEM, err := os.ReadFile(s.crtPath)
	if err != nil {
		return fmt.Errorf("failed to read CA cert: %w", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("failed to parse CA cert PEM")
	}
	s.caCert, err = x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA cert: %w", err)
	}

	// CAの秘密鍵を読み込む
	keyPEM, err := os.ReadFile(s.keyPath)
	if err != nil {
		return fmt.Errorf("failed to read CA key: %w", err)
	}
	block, _ = pem.Decode(keyPEM)
	if block == nil {
		return fmt.Errorf("failed to parse CA key PEM")
	}

	s.caKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		s.caKey, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return fmt.Errorf("failed to parse CA key: %w", err)
		}
	}

	return nil
}

// HandleConnection は、指定された接続に対して SSL Bump (MitM) を実行します。
// クライアントとのTLSハンドシェイクを完了させた接続（tls.Conn）を返します。
// 呼び出し元は、返された接続を使ってHTTPリクエストを読み書きし、最後に閉じる責任があります。
func (s *SSLBumpHandler) HandleConnection(conn net.Conn) (net.Conn, error) {
	// 動的な証明書生成を行うためのTLS設定を準備
	config := &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return s.generateCert(hello.ServerName)
		},
	}

	// 接続をTLSサーバーでラップする
	tlsConn := tls.Server(conn, config)

	// Perform handshake
	if err := tlsConn.Handshake(); err != nil {
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}

	return tlsConn, nil
}

// 指定されたホスト名に対して動的に証明書を生成するメソッド。
func (s *SSLBumpHandler) generateCert(hostname string) (*tls.Certificate, error) {
	if hostname == "" {
		hostname = "unknown"
	}

	// キャッシュを確認
	s.cacheMu.RLock()
	if cert, ok := s.certCache[hostname]; ok {
		s.cacheMu.RUnlock()
		return cert, nil
	}
	s.cacheMu.RUnlock()

	// サーバー証明書用の新しいRSAキーを生成する(事前に生成されたDH鍵を使うよりも安全！)
	// 高速化のため、事前に生成した共通鍵を使用する
	priv := s.sharedKey

	// 証明書テンプレートを作成する
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			// hostを偽証するためにCommonNameに設定
			CommonName:   hostname,
			Organization: []string{"ORF 2025 Space Proxy"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour), // Valid for 24 hours

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{hostname},
	}

	// CA証明書と鍵で証明書に署名する
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, s.caCert, &priv.PublicKey, s.caKey)
	if err != nil {
		return nil, err
	}

	// 証明書と鍵をPEM形式にエンコードする
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	// tls.Certificateを作成する
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	// キャッシュに保存（FIFO: 古いものから削除）
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// ダブルチェックロックパターン: ロック取得待ちの間に他が生成したか確認
	if cert, ok := s.certCache[hostname]; ok {
		return cert, nil
	}

	// サイズ制限チェック
	if len(s.certCacheKeys) >= s.maxCacheSize {
		// 最も古いキー（先頭）を取得・削除
		oldestKey := s.certCacheKeys[0]
		s.certCacheKeys = s.certCacheKeys[1:]
		delete(s.certCache, oldestKey)
	}

	// 新しいキーを追加
	s.certCache[hostname] = &tlsCert
	s.certCacheKeys = append(s.certCacheKeys, hostname)

	return &tlsCert, nil
}
