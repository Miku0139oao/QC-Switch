# 驗證紀錄

## 本機環境

2026-10-18，Windows x64；Go 1.26.3、Flutter 3.47.7、Dart 3.13.5、Visual Studio 2022 C++ Build Tools。

## 自動化與模擬上游

- `go test ./...`、`go vet ./...` 通過。
- `flutter analyze`、`flutter test`（7 項）通過。
- `flutter build windows --release` 通過。
- 六種路由各驗證 JSON、SSE 與完整工具歷史重播。
- 中文／emoji 跨位元組、SSE CRLF／多行、分片工具參數、並行工具、結果、多輪、圖片／推理、custom tools／namespace、取消、斷流、串流閒置逾時及不重送。
- 熱切換保留既有請求快照，下一個請求使用新設定。
- 設定目錄覆寫與環境變數、既有 Codex auth、TOML 註解、原子寫入失敗、外部修改、同欄位衝突、崩潰恢復與加密資料檢查。
- 啟動管理核心、直連、取得模型和連線測試不建立 listener；本地路由只綁定 loopback。
- Flutter 六頁導覽（1240×820、960×640）、真實 RPC 參數、明確測試操作、逐欄位衝突、事件更新及主題狀態。

## Portable 原生操作

實際執行 Windows release portable 程式，使用獨立 `user-data`、客戶端設定目錄與本機 Chat 模擬上游，未接觸原有客戶端設定或真實 Key：

- 在 UI 啟用 Codex 本地路由，透過寫入的本機憑證完成 Responses 串流轉換。
- 關閉視窗後，UI 與核心仍存活，路由持續完成另一個串流請求。
- 再次執行 portable 程式會顯示原有視窗；仍只有一組 UI／核心程序。
- 按「還原設定並退出」後，客戶端檔案逐位元組恢復原狀，本機 listener 停止，兩個程序結束。

上述測試使用手動 UI 操作搭配獨立模擬上游斷言。系統匣各項選單仍需額外人工驗收。

## 實際客戶端，模擬上游

**Codex CLI 0.153.4：通過。** 使用獨立暫存 `CODEX_HOME` 與工作目錄，依序連到 Responses、Messages、Chat 三個本機模擬上游。每組四次請求涵蓋模型目錄、文字串流、實際讀檔、PowerShell 命令、custom `apply_patch` 建檔、工具結果與後續回合。測試不使用真實 Key，也不寫入使用者原有設定。

Codex 模型目錄提供舊版欄位與目前的 `model_messages` 相容欄位；目前僅上述版本經實際執行驗證。其他版本、VS Code 擴充套件和 Windows 桌面版的 schema／行為仍需實機驗證。

## 尚未完成的外部驗收

沒有提供 QuickRouter Key，因此未進行真實上游呼叫、Key 權限、計費 token 或餘額驗證。Claude Code CLI 尚未安裝；Claude Code VS Code、Codex VS Code、Codex Windows 桌面版亦未進行實際讀檔／命令／修改／後續回合驗收。

這些結果與模擬測試分開記錄，不宣稱已通過所有客戶端或所有 QuickRouter 模型的相容性。實際測試時應記錄客戶端版本、所選模型、上游協議及每一項結果；請在測試專用目錄內進行工具操作。
