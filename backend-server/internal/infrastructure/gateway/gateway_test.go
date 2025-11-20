package gateway

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestManualDTNFlow(t *testing.T) {
	// 修正1: 第3引数は time.Duration なので nil ではなく時間を指定
	// (このテストでは gw 自体は使いませんが、コンパイルを通すために正しい型を渡します)
	_ = NewBpGateway("example.com", 80, 30*time.Second)

	// テストリクエスト作成
	reqBody := "This is a test request from Go test"
	req, err := http.NewRequestWithContext(
		context.Background(),
		"POST",
		"http://example.com/api/test",
		strings.NewReader(reqBody),
	)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	fmt.Println(">>> Starting DTN Send/Receive Test (via sendrequest) >>>")

	// 修正2: gw.sendAndReceiveBundle(req) ではなく sendrequest(req) を直接呼ぶ
	// (現在の gateway.go では sendrequest は BpGateway のメソッドではなく、パッケージ内の独立した関数として実装したため)
	resp, err := sendrequest(req)
	
	if err != nil {
		t.Fatalf("DTN communication failed: %v", err)
	}
	defer resp.Body.Close()

	// 結果検証
	fmt.Println("<<< Response Received <<<")
	fmt.Printf("Status Code: %d\n", resp.StatusCode)
	
	respBody, _ := ioutil.ReadAll(resp.Body)
	fmt.Printf("Response Body: %s\n", string(respBody))

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}