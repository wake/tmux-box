# Changelog

## [1.0.0-alpha.333] - 2026-09-08

### Feat: 分頁自己去問「這個 pane 是誰的」，resume 指令變成可設定的範本（#977）

alpha.332 之後留下兩個洞。一是**部署前就開著的 session 重建只會得到純 shell**——紀錄的 agent 只由合格的 `SessionStart` 寫入，而那些 session 的 `SessionStart` 早在新程式上線前就發生過了，使用者實際看到那個空欄位。二是組出來的 `claude --resume <id>` **對他是叫錯執行檔**——他用的是一個 zsh function（會補上 `--dangerously-skip-permissions` 與 iTerm2 狀態指示），所以重建會 resume 到對的對話、卻用錯的程式啟動。

**第一版設計整個被推翻，而且推翻得對。** 原本是 daemon 在任何 owner 事件上主動掛第二個 envelope，並節流成「每個 frame 一次」。review 指出兩個缺陷，兩個都是「推」這個形式本身的問題，不是節流參數調不好：廣播出去時使用者往往還沒開那個分頁（他會先開 Purdex 再開 tab），那一次資格就這樣消耗掉了；而「只填不覆蓋」的寫入無法被後續同樣「只填不覆蓋」的寫入更正，所以一旦寫錯就定型。改成**由 SPA 主動查詢**之後，兩者同時消失——答案是給發問者的 response，不會沒人接；而且隨時可以再問。節流機制、arming map、混版風險也一併不需要了。

**ownership 判定沒有動。** 仍然是 frame layer 的 ancestry walk，從 `classifyAncestor` 抽成共用函式而非另寫一套，外加每個被接受的事件本來就會通過的 pane process tree 檢查。`agent_frames` 補上它從來沒存過的 session id，由**任何** own-frame hook 事件透過專屬的窄 UPDATE 寫入——那個 UPDATE 不參與任何 read-modify-write，所以 proxy attach 的 retry loop 不會把它洗掉。

**`resolvePanePID` 修的是一個既有的 bug，而且專案自己記錄過。** `PanePID` 走 `tmux list-panes -t <target>`，tmux 會把 pane id 解析成它所屬的 window，於是回傳的是**第一個** pane 的 PID。`liveness.go` 的註解在 PR #638 就寫清楚過並改用 `ActivePanePID`，但 `verify.go` 沒跟上——所以分割視窗裡非第一個 pane 的 hook 一直被判成 `pid_not_in_pane_tree` 而拒絕，那些 pane 根本不會有 frame。這是本功能的必要前提（查詢要求走訪途中看到 panePID），也意味著那些 pane 從此會開始產生先前不存在的 frame。

**shell probe 只驗指令的第一個 token，而且必須在互動 login shell 裡驗。** 一個 shell function 對任何非互動 shell 都是不存在的——那正是這個功能要解決的情境。`type` 的輸出在 zsh 與 bash 不同（bash 還會印出整個函式本體），`command -v` 的輸出也不是「路徑或單字」（alias 會印出自己的定義、PATH 上的相對路徑會印相對路徑），所以 API **不宣稱找到的是哪一類東西**，只回報解析成功與 shell 實際印出什麼。

**五輪 review、24 項發現、23 修 1 延後。** 其中四項是 plan 本身寫錯——`op.created` 這個符號不存在、空指令是關掉 resume 步驟而不是排除整個 pane、`field` 的同值提早返回會讓「確認同一個目錄」標不上使用者來源、`Executor` 根本沒有列舉 session panes 的能力。這些都在進到程式碼之前就被攔下來。另外四項是**測試綠了卻沒驗到東西**，每一項都靠變異測試才確認：deadline 只在一開始就過期時測、partial slice 恰好是空的所以分辨不出有沒有丟棄；disowned 的測試從未建立 deferred timer；單一 pane 的取消測試分辨不出「回傳部分結果」與「整個丟棄」。

**兩個實測推翻了 review 的診斷。** bash job control 那項，第二輪說「`-i` 下沒有 `m`、要靠 rc 裡的 `set -m` 才會逃出 group」，第三輪量到相反結果。最後用一支複製 probe 實際 fd 配置的 Go harness 量了七種組合才釐清：決定性變因是 **bash exec 時是否已是 process-group leader**（`Setsid` 與 `Setpgid` 都會讓它是），而且**在 bash 還在 source 啟動檔期間啟動的背景 job，不論 `$-` 有沒有 `m` 都會留在 shell 自己的 group**。兩邊的結論都對、理由都錯，註解現在三個事實都寫進去。

清理仍有做不到的邊界，並且寫在 spec 裡而不是宣稱解決：自行呼叫 `setsid()` 的 descendant、`ps` 快照之後才 fork 的程序、`Getsid` 到 `Kill` 之間被縮小但未關閉的 PID reuse 窗口。

**已知限制**：部署後那個 session 必須**至少送出一次 hook**，daemon 才知道它的 session id——還在用的 session 下次互動就會補齊，之後完全沒再碰過的則維持純 shell（但有 cwd）。範本是所有 host 共用的，只有測試是選 host 的。probe 近似 pane 的 shell，不是重現它（沒有 tty、不跑 tmux 的 `default-command`）。

延後追蹤：#978（walker 把讀取失敗與「不是 root」混為同一個 verdict）、#979（`PanePID` 其餘呼叫者稽核）、#980（清空範本會安靜退化成純 shell）、#981（SPA 既有 flake）、#983（rebuild 規則堆積在 `useTabStore`）。

## [1.0.0-alpha.332] - 2026-09-07

### Feat: 每個分頁記住自己怎麼被重建（#970）

使用者的需求：主機重開之後 tmux session 全滅，希望每個崩掉的分頁能「一鍵還原」——記住自己的 pwd、tmux id、跑的是 claude/codex/opencode，以及對應的 resume 指令。

**先量再寫。** 三家 agent 的 hook payload 全部實地觀測，沒有一項靠推論：cc 與 codex 的 `SessionStart` 從 `agent_trace_steps` 逐字撈出，兩者都帶 `session_id` + `cwd`；opencode 的則是實跑一次才拿到——它**只**帶 `session_id`，所以 `cwd` 只能從 plugin 的 `PluginInput` 取，而且 `session.created` 只在第一次送出 prompt 時才發，TUI 啟動不發。六個 resume flag 全部用 `--help` 驗過。

**判定「這個 SessionStart 屬不屬於這個 pane」花掉最多來回。** spec 三輪 review：第一輪要求把判定從 frame layer 移到 process ancestry（因為同型巢狀 `cc` 裡開 `cc` 不走 proxy 收摺），第二輪指出那會踩到 launcher 反例——`codex.js` 用 `spawn` 不是 exec-replace，node launcher 全程是 native codex 的父程序，而 `codex.Identify` 對 argv 含 `/codex/` 的 JS runtime 回 true，所以**每一個 npm 裝的 codex 都會被判成巢狀在自己裡面**，永遠不記錄。實測確認後 v3 退回 frame layer：frame 只由 hook sender 建立，launcher 從不送 hook 所以永遠沒有 frame——「要求祖先有 frame」正是讓判定安全的那個條件。最終只把既有走訪那個被過載的 `(nil, nil)` 拆成四態，並把 envelope 掛在**變更結果**而非預先判定上（`applyFrameEvent` 在 Upsert 之後還有第二次 proxy 收摺會撤銷 root 判定）。

實測數據也顯示原本擔心的 subagent 情形在事件層根本不會發生：cc `PdxSubagentStart` 21 次 vs `PdxSessionStart` 16 次、codex 9 vs 270，subagent 走獨立事件；跨型巢狀已被既有 proxy 收摺擋掉 45 次。真正的洞只有同型巢狀一種。

**世代（tmux server identity）貫穿整條路徑。** session code 是 tmux `$N` 的可逆編碼，重開機後 `$0` 會鑄出同一個 code，所以 pane 可能靜默接到陌生 session。`tmux_instance` 現在蓋在每個 `Session`、每個 provenance envelope 上，並**併入 watcher 的 hash**——否則「重啟後重建出完全相同的清單」不會觸發廣播，SPA 會永遠停在舊世代。`tmuxInstance` 這個宣告已久卻從無人寫入的欄位，同時被補實。

**PR 四輪 review、20 項發現、19 修 1 延後。** 反覆出現的其實是同一條規則的兩個方向：**沒有證據就不動作**——世代不明時不宣告 pane 已死（避免誤殺），世代不明時也不授權寫入或送指令（避免誤傷）。前三輪的每個洞都是把前者的「不判死」誤讀成後者的「可執行」：cwd probe 用**問的時候**的世代寫入回應值、retry 拿 SPA 快取當授權、`created.tmux_instance` 為空時直接省略前置條件。修法是給 daemon 兩個前置條件（`/cwd` 回傳與 cwd 同時採樣的世代、`send-keys` 接受 `expected_tmux_instance` 不符回 409），並讓 `/cwd` 在 pane 讀取的**前後兩側**各採樣一次、不一致就回空——`GetSession` 原本是在 pane 讀取之前蓋章，中間重啟會把新伺服器的 cwd 配上舊戳記交出去。

**送鍵的檢查與送出必須是同一次 tmux 呼叫。** 分成兩次呼叫的任何做法都留有窗口，再採樣一次也沒用，因為 `SendKeysRaw` 會重新連線並按名稱解析目標。改用 `if-shell -F` 讓**收到按鍵的那台 server 自己評估條件**：server 若重啟，指令連到的是新 server，`#{pid}:#{start_time}` 不符即走 else 分支。按鍵以 hex 送出（巢狀指令字串會被 tmux 二次解析）、目標用 session id 而非名稱、sentinel 是唯一判決通道（`if-shell` 兩分支都 exit 0）。對真 server 實測過相符與不符兩路。

**入口三處**：崩掉的 pane 上三列可勾可改的動作集 + 一顆 Rebuild；double-click 分頁名稱展開同樣三項（活著的 session 也能看能改，split tab 每個 pane 一塊）；Settings → Snapshot 的批次表，依 `(hostId, tmuxInstance, sessionCode)` 分組，兩個 pane 指向同一個死 session 只會建一個。舊的 snapshot capture／layout restore／undo **保留**——它們是另一件事，能力邊界寫在 spec §4.11：legacy 動作只開 shell，永遠不執行 agent 指令。

**行為變更**（非功能本身、但你會看到）：原本靜默接錯 session 的 pane 現在會正確顯示成 terminated；opencode plugin template 改了而 `CheckHooks` 是 byte-compare，已安裝的 plugin 會判定 drift 需重裝；terminal attach 現在要等該連線的第一份 session payload 才建立。另外修掉一個既有 transport bug：`connect()` 在 ticket 飛行中提前返回，導致 `reconnectWithTicket()` 存進的新 ticket 從未被使用、舊 ticket 反而開了 socket。

延後：`SnapshotSettingsSection` 拆檔（#975）。既有缺陷另開 #971 / #972 / #973 / #974。

**尚未做**：重開機路徑無法單元測試，plan 末尾有 6 步手動驗證清單，需要在 Air 上真的 `tmux kill-server` 跑一次。


## [1.0.0-alpha.331] - 2026-09-07

### Refactor(store): trace store 依職責拆分（#968）

純重構、無行為變更。清掉 #962 的 file-health review 留下的三項體質債（#963 / #964 / #965）。

**原始碼**：`internal/store/trace.go` 1,195 行同時承載型別、schema DDL、migration、legacy rebuild、CRUD、保留期清理、去重規劃、cursor 編解碼與 JSON helper，拆成四檔——`trace.go`（89，型別與介面）、`trace_migration.go`（550）、`trace_write.go`（358）、`trace_read.go`（247）。pruning 放 write 而非 migration，因為它在 `SaveChain` 的同一交易內；`rawJSONText` 放 write，因為只有寫入路徑用它。同 package，不新增 service 層或 interface。順帶刪除 `firstNonEmpty`——整個 repo 沒有任何呼叫者。

**測試**：兩檔重整成五檔，**fixture 依實際呼叫者分配而非名稱前綴**。單一主題專用的跟著主題走（`seedLegacy*` / `seedIntermediate*` 全部只服務 migration），只有真正跨檔共用的才進 helpers，因此 helpers 收斂到 144 行。restart 測試歸 migration，理由是它釘的正是「重跑 migration 不得破壞已去重資料」。

**`planTraceRootDedup`**：回傳型別從 `(string, []bool)` 改為 `tracePayloadPlan{RootPayload, Steps []storedTracePayload}`。這消除重複的 `rawJSONText` 轉換並把 payload 與旗標綁在一起，但**沒有消除位置耦合**（codex 指正我原本的宣稱）——`SaveChain` 仍以 `steps[i]` 配 `plan.Steps[i]`，該契約改為明訂：等長同順序、不得單獨重排、`RootPayload` 不可留 struct 零值、candidate 需在清空任何項目前捕獲。

**怎麼證明是純搬移**：行數持平證明不了什麼——SQL、斷言或條件都可能在總行數不變下被改掉。改用逐宣告內容比對：從 `origin/main` 與工作樹各自抽出所有頂層宣告依名稱配對，**101 個宣告位元組完全相同**，差異只有五項預期變更。測試名稱清單前後一致，75 個無遺失。

Codex 兩輪 review 皆無阻擋性發現。對抗性額外抓到一個歸屬瑕疵並已修：`assertRawShape` 在共用 helpers 卻依賴被我放進 dedup 檔的 `readRawTraceRoot`（成因是我 grep 時排除了 helpers 檔本身，漏看這個呼叫者）。

## [1.0.0-alpha.330] - 2026-09-07

### Perf(store): agent trace 的 root payload 每條 chain 只存一次（#962）

使用者問「這些 event 資料有長期／短期保留的必要嗎」，查下去發現問錯的不是保留期，是每筆存了什麼。

`agent_trace_steps` 每列存三個 JSON blob。live 資料庫實測（10,000 chains / 39,554 steps / 396 MB）：`payload_json` 總計 **324.9 MB**（平均 8.2 KB、單筆最大 **682 KB**），而 `before_json` + `after_json` 加起來才 7.2 MB。也就是 `payload_json` 幾乎就是整個資料庫。

而其中 **73% 是重複**：去重後（`DISTINCT chain_id + payload_json`）只有 86.3 MB。根因是 `trigger` / `verify` / `frame` / `projection` 四個 step 都把整個 `req`（`EventRequest`）當自己的 payload，四份位元組完全相同；`PdxPostToolUse` 特別肥是因為 payload 帶著 tool result，可能是整個檔案的內容。

**保留機制本來就有**（`pruneTraceChains` 上限 10,000 chains / 100,000 steps，表卡在 10,000 就是它在運作，涵蓋約 32 小時），所以這輪不動保留期。

- `agent_trace_chains` 加 `root_payload_json`、`agent_trace_steps` 加 `payload_is_root` 旗標，共用的 root payload 每條 chain 只存一次。
- 以 `rawJSONText` 的輸出（stored form）比對，**只有 bytes 相等才合併**；語意相同但 bytes 不同的保留各自副本，只降低去重率、不影響正確性。
- 讀取用 **JOIN + `CASE WHEN`** 在 SQL 內解析，而非分兩次查詢在 Go 裡拼——後者會讓交錯的 `SaveChain` 把 root B 的 chain row 配上 root A 的 step rows，靜默回傳錯的 payload。**API 輸出位元組完全不變**，SPA 不需改動。
- 新欄位**刻意不列入** `needsChainRebuild` / `needsStepRebuild` 的 required 清單：列進去會讓現行 schema 全部走 legacy rebuild，而該路徑的 copier 讀現行表沒有的 `created_at` / `agent_type`，migration 會直接失敗。兩個 regression 測試釘住這件事。
- 既有資料不 backfill，靠 pruning 汰換。

24 個新測試全程 TDD。涵蓋部分去重 `[A,B,B]` / `[A,A,B,B]`、位元組保真、亂序輸入與 `Seq` tie-break、四種空值慣例、re-save 轉換、step INSERT 失敗的 rollback、migration 矩陣、重啟後已去重資料仍可讀、混合新舊 chain 的 pruning。交錯讀寫用 `sqlQuerier` 接縫做成決定性測試，並**實作過錯誤版本確認它真的會紅**。

Codex 四輪 review（標準 + 攻擊／防守／體質三平行）全部 approve 無實質發現。體質提出的註解措辭問題已修；拆檔、測試重整、`planTraceRootDedup` 回傳型別三項延後至 #963 / #964 / #965。

**⚠️ 部署限制**：這是磁碟格式變更，**舊版 daemon 不能讀已被此版本寫過的資料庫**——舊 reader 只讀 `payload_json`，會把去重過的 row 讀成空 payload，靜默不報錯。回滾只能用仍懂新格式的版本，或還原升級前備份。

延後項目見 #957（payload 截斷：768 列 >64 KB 佔 241 MB，但那是有損的）。

## [1.0.0-alpha.329] - 2026-09-07

### Fix(daemon): `pdx start` 健康檢查窗口修復（#960）

開機後 purdex 沒起來，表面像是重開機造成的，實際上跟重開機無關 —— 是既有問題被資料庫大小推過了臨界點。

`pdx start` spawn 出 `pdx serve` 之後，只用固定的 `500ms + 5×200ms ≈ 1.5s` 探測 `/api/health`，探不到就 SIGKILL 子行程並 exit 1。而 `agent_events.db` 已經長到 1.1 GB，冷開機時檔案不在 page cache，光是 SQLite 開檔就超過這個窗口 —— **daemon 被自己的啟動指令殺掉**，只留下一個指向死 pid 的 `pdx.pid`。因為是 SIGKILL，沒有 panic 也沒有 crash report，看起來像是無聲不啟動。

這個窗口是個跟「啟動實際需要多久」無關的固定預算，資料庫越大越容易踩到。

- 抽出可測試的 `waitForHealthy()`：窗口 1.5s → **60s**，200ms 輪詢。
- 用一條跑 `cmd.Wait()` 的 goroutine 偵測子行程提早退出 —— 真的啟動失敗時立刻回報，不會白等滿 60 秒（沒有這層，延長窗口只會讓失敗變慢）。
- 整個等待綁在一個 `context.WithDeadline` 上，probe 改用 `http.NewRequestWithContext`；每次 probe 前先檢查 deadline，單次上限取 `min(5*interval, 剩餘時間)`，沒有任何一次 probe 能活過窗口。
- 接受 200 之前再複查 `childExited` 與 deadline —— 子行程送出 200 後隨即崩潰、或 200 落在窗口外，都不會被誤判成啟動成功。
- 失敗訊息區分「子行程退出」與「窗口耗盡」，取代原本含糊的 `health check failed, killing child`；等超過 3 秒印進度行。
- 探測改用帶 timeout 的 request（原本 `http.Get` 無 timeout，daemon 接了連線但 hang 住會讓整個窗口失效），並移除無條件的 500ms sleep。

端對端實測：子行程 bind 失敗即死，舊版 1.535s + 含糊訊息 → 新版 0.47s + 指出根因；正常啟動 0.21s（比舊版快）。

Codex 三輪 review（標準 → 對抗性 → 複查），對抗性攔下兩條：接受 200 前未複查子行程退出、deadline 到了仍發動完整長度 probe（最壞總耗時可逼近 `2×timeout`，且窗口外的 200 會被判成功）。

同時手動 `VACUUM` 把 `agent_events.db` 從 1.1 GB 縮到 389 MB（其中 730 MB 是舊 FK leak 刪除後未回收的 free pages）。成長來源未修，追蹤於 #957。

延後項目：#957（`agent_trace_steps` 無保留期限）、#958（失敗時只 Kill 子行程未殺 process group）、#959（健康檢查無法分辨 200 是否來自自己 spawn 的 daemon）。

## [1.0.0-alpha.328] - 2026-08-19

### Fix(editor/storage): in-Purdex 檔案編輯四大問題修復（#940 / #947 / #952）

使用者回報 in-Purdex 檔案編輯的四個問題，調查後拆成三個 PR 依序處理。全程 spec → codex 審 → plan → codex 審 → subagent TDD → PR 兩輪 codex review 的完整流程，三個 PR 共經 **10 次 codex review**（含 3 組 3-parallel 對抗性），攔下 **13 條會靜默毀損或誤刪使用者檔案的路徑**。

**PR-A #940 — 遠端檔案資料安全 + 近期檔案重映射 + 儲存提示**

- 「遠端檔案開起來少內容」與「所有遠端檔案都顯示有變動」是**同一個 bug 的兩面**：daemon backend 忽略 `source.hostId`（打到 active host）＋ 讀取失敗靜默開空 buffer（`lastStat` 為 null，而 dirty 燈號綁的正是含 `!lastStat` 的 `canSave`）。
- `getFsBackend` 加 resolver 層依 `source.hostId` 綁定；載入失敗改為錯誤態 + Retry，**不建立 buffer**，因此不可能存檔覆寫真實檔案；dirty 燈號改綁 `isDirty`。
- 近期檔案補 `renamePath` / `removePath`，接上 rename / move / delete / 編輯器內改名（含遠端）。
- 儲存結果 toast：已儲存 / 沒有變動需要儲存 / 儲存失敗（含原因）。
- review 攔下：host 被移除時 `getDaemonBase` 仍 fallback 到 active host（wrong-host 防線在邊界失效）、**untitled 首次儲存輸入既有檔名會直接覆寫該檔**（既有 bug）、write 成功但 stat 失敗誤報儲存失敗。

**PR-B #947 — Markdown Live Mode 無損化**

Live Mode（Tiptap）的文件模型是 markdown 的有損子集，**損壞發生在 parse 當下**，編輯一個字就會把有損結果寫回。實測修復前：表格整段變空字串、front matter 變 heading、raw HTML 被剝除、task list checkbox 消失、CRLF 轉 LF、檔尾換行被吃。

- 加入 TableKit / TaskList / TaskItem / Image 擴充，表格與 task list 現在能安全編輯（附表格浮動選單）。
- 新增 round-trip 安全閘門（marked token 白名單 default-deny + front matter / footnote / HTML entity / 混合行尾的專項偵測），無法無損表達的文件改以 raw 開啟並在工具列說明原因。
- 保留原始行尾、檔尾換行與前導空行（新增載入後不可變的 `sourceEol` / `sourceTrailingNewline` / `sourceLeadingBlankLines`）。
- 閘門依 `savedContent` 而非即時 `content` 評估，打字不會在編輯途中抽換編輯器。
- 對抗性 review 用真實 Tiptap 實測攔下 4 條靜默毀損：**圖片被吃成純文字**（白名單宣稱支援 image 但 schema 根本沒有 image node）、HTML entity 雙重 escape、混合行尾被洗成單一、純空行檔案行數折疊。另補 `image-in-mark`（`[![badge](x)](url)` 失去連結）與 `bracketed-url`（含空白的 destination）兩個 blocker。

**PR-C #952 — Storage 操作 + placeholder 自動清理**

- 每列 hover 操作（Open / Rename / Delete，作用於該列而非目前選取）、可見的批次選取（checkbox + 全選三態 + 動作列）、清除 0 B 空檔。
- 大量 0 B `Untitled-N.txt` 的成因是 New File 的 eager reservation（一按就落地真實空檔）。新增 placeholder registry 記錄「這個路徑是我們建的、使用者還沒碰過」，在最後一個引用消失時自動清除。
- 明確否決「靠 buffer 形狀推論」的原方案（會誤刪「本來有內容、被清空後存檔」的檔案）；刪除判斷基於 **post-`closePane`** 的剩餘引用，而非元件卸載（否則 pane 移動會誤刪）。
- review 攔下：registry 缺跨分頁同步（A 分頁存檔後 B 分頁會刪真實檔案）、外部變更 reload 未除籍、Clean Empty 用舊快照刪除、批次刪除只顯示數量不顯示路徑。最後加上「刪除前 re-stat 確認仍是 0 B」的結構性防線。

**順帶修掉的死 class**：`text-accent-base` 與 `bg-surface-selected` 在 Tailwind 4 沒有對應 token（build 產出 CSS 命中 0 次）—— 後者導致 **Storage 選取中的列一直以來沒有任何高亮**。

**體質**：`EditorPane.tsx` 732 → 389 行（抽 4 個 hook）、`EditorPane.test.tsx` 2025 行與 `StoragePane.test.tsx` 1981 行拆分、`remapPanesUnder` 職責分離、抽 `lib/path-remap` 共用規則。三次純重構均驗證測試總數不變、零斷言修改。

測試 3983 → **4365**（+382）。lint / build / `go test ./...` 全綠。

**Follow-up**：#941 restore 後近期檔案失連 · #942 `text-accent-base` repo-wide · #943 `getWsBase` 同類 host fallback（WebSocket 可連錯機器）· #944 untitled 改名後 ⌘S 撞名靜默中止 · #945 flaky test · #946 `openRecentEntry` 收斂 · #948 footnote 偵測誤判 code fence · #949 save/rename hook 耦合 · #950 `round-trip-safety.ts` 拆分 · #951 Live Mode 支援連結包圖片（README badge）· #953 placeholder 除籍收斂 · #954 Storage 檔案過大 · #955 路徑快照 ABA

## [1.0.0-alpha.327] - 2026-08-18

### Fix(spa): New Tab「Bring in an open tab」限高半窗並內部捲動

分割視窗時右側 new-tab pane 的 Bring-in 區塊無高度上限（`flex-shrink-0`），開啟的 tab 一多就整段撐長，把下方 provider grid（Sessions / Files…）擠出可視範圍。

改為 `max-h-[50%]` + `min-h-0` 的 flex column：標題固定、候選清單 `overflow-y-auto` 內部捲動；保留 `flex-shrink-0` 讓候選少時仍維持自然高度。附 regression test（20 個候選 tab）。

## [1.0.0-alpha.326] - 2026-07-19

### Feat(m0): Ploom↔Purdex 派工整合 — 設計基礎 + Purdex 執行消費端 (#933, #937)

M0 walking skeleton 的 **Purdex 側**：Ploom issue 派工 → daemon 輪詢領工 → 在指定 repo 起 `claude -p` → 狀態/diff 回報 → deeplink 觀看。Ploom 側為獨立 PR（另一 repo）。

**設計基礎 (#933，docs-only)**：M0 spec（3 輪 codex 深審，findings 8→4→1→0）+ implementation plan + **共享 wire contract SOT**（`docs/specs/m0-contract.md`）+ 16 個 golden fixtures（`docs/fixtures/m0/`），作為兩 repo 各自 mock 的唯一真相。

**執行消費端 (#937，12 個 TDD task)**
- **傳輸**：pull 模型 —— `GET /daemon/dispatches?status=pending` → claim → 兩段式 fetch → report；Ploom 純 server 無 callback。
- **execution runtime SOT**：`execution_id` 為唯一對外 handle；狀態機 + `dispatch_id` 冪等；`GET /api/execution/{id}` 唯讀投影。
- **Crash-consistency**：狀態轉換與 report enqueue 在**同一 SQLite transaction**（outbox 併入 execution DB）；launch fence（`launch_state`）防重複 launch；`session_name` 為 crash-recovery handle（`HasSession` 探活、by-name 收孤兒）；startup reconcile sweep + manual reclaim（`POST /api/dispatch/reclaim`）；ack cursor + accepted-before-lifecycle ordering + 重啟 replay。
- **Terminal 兩來源**：process-exit 決定**時點**（權威），`result.is_error` 決定**成敗**；exit 0 但無 result → completed(`exit_only`，degraded 並記錄來源）。
- **Admission**：canonical repo key（EvalSymlinks 防別名繞過）+ per-repo lock 跨 accept→launch（防 TOCTOU）+ status-based 單一 live execution + `head_at_start`/`dirty_at_start` 快照。
- **安全**：`dispatch.allowed_repo_roots` **fail closed**（未設則一律拒，建立 repo 信任邊界）；sandbox profile 全序 clamp（只降不升）映射 `claude --permission-mode`；派工 prompt 走 **relay stdin stream-json**（不進 tmux 指令列，零 injection）；缺依賴時停用 consumer 而非 claim-and-drop。
- **Deeplink**：`purdex://execution/<id>` OS protocol handler（single-instance / open-url / 冷啟動 buffer / 單一落點視窗）+ SPA execution route 與 **observe-only** 詳情頁（不掛 stdin 寫入）。
- **Artifact**：pointer-first（diff `{files,add,del}` 摘要 + transcript pointer，不 inline blob）。

**Review**：codex 標準 review（3 項全修）+ 3-parallel adversarial（攻擊/防守/檔案體質，三份皆 needs-attention）收斂出 6 項，5 項修復、1 項（DB 層單-live guard，M0 單 daemon 前提外）→ issue #938。

`go test ./...` 全綠 / vitest 3982 / lint / build 綠。

## [1.0.0-alpha.325] - 2026-07-19

### Fix(opencode): child (subagent) session 事件不再劫持父 session 燈號 (#934)

opencode 每次 subagent（Task tool）完成，父 session 的燈號會錯誤塌成 idle（甚至變紅／frame 被刪），即使父 session 還在跑。根因：opencode 把每個 subagent 開成獨立 **child session**，plugin 之前把 child 的每個生命週期事件（created/idle/error/deleted）都當父 session 的 `Pdx*` 事件 emit；daemon 用 `(pane, senderPID, senderStartTime)` 比對 frame（**非** opencode session_id），而單一 opencode process 的父子共用同一 pane／sender identity，所以 child 事件全落在父 frame 上。

- **修法（純 plugin，daemon/SPA 不動）**：plugin 維護 `subagentSessions = Map(childSessionID → parentSessionID)`，從 `session.created` 的 `info.parentID` 學習，gate 掉 known child 的全部 parent-level emit。subagent 的真實表徵仍由 `PdxSubagentStart/Stop`（detail-only，不搶 frame）提供。
- **reload-proof delete**：`session.deleted` 也 publish 完整 info（`session.ts:624`），故 child delete 直接用事件自帶 `info.parentID` 判定，即使 plugin reload 期間漏收該 child 的 created，也不會誤刪**父** frame。
- **防禦**：`sid = sessionID || info.id` fallback；空 sid 不入 map；parent delete 只清 value 相符的 children（單 process 可有多 root session）。
- **已知殘留（#935 追蹤）**：`session.status` idle 與 `session.error` 上游確實不帶 parentID（opencode #30043），故 plugin reload 中途的罕見窗口仍可能漏一次假 idle（`notification_silent`、可自我修正）／假 error；非回歸，且遠窄於修前的「每個 subagent 都漏」。
- 上游 schema 對現行版本查證（`session.status` 無 parentID、`session.created`/`session.deleted` 帶完整 info）；codex spec+plan+PR 標準+對抗性 review 共 4 輪，抓修 reframe（僅擋 idle→擋全生命週期）、`Set`→`Map`、reload-window delete、空 sid 污染等；Go template + `pluginSimState` mirror 雙軌 + Bun 真 JS 序列測試；全套 go test（含 `-race`）綠。

## [1.0.0-alpha.324] - 2026-07-14

### Feat(snapshot): Sessions 對帳表手動編輯 cwd（rebuild 落點）(#931)

在 Snapshot Sessions 對帳表 double-click **Directory** 欄，手動修正/補上該 session 重建時的目標 cwd。純 client-side 改快照，不碰 daemon、不動 live session——只改「Rebuild all / Restore everything 對該 session 傳給 createSession 的 cwd」。承接 alpha.321（cwd 改用 pane_current_path），補上使用者直接控制落點的能力。

- **所有列可編**：⚠️ 無 cwd 者補上路徑即變可重建（restorable:true）；🟢 live 設將來死掉的落點；空值清成 structure-only。
- **`setSessionMetaCwd` 純函式**（storage.ts）：trim；非空 → cwd+restorable:true+清 captureError（手動值權威，蓋過 cwd-probe-failed/dead）；空 → cwd:undefined+restorable:false；複合鍵 `[host][code]` 定點更新、不 mutate；維持 `restorable ⟺ 有 cwd` 不變式（與 capture / computeHealth 🔴 predicate 一致）。
- **`EditableCwdCell`** inline edit：double-click 進編輯（預填現值）、Enter/blur 存、Esc 取消；committedRef latch 防 Enter-then-blur 雙提交。存檔走 readSnapshot→setSessionMetaCwd→writeSnapshot→既有 refresh() 重讀+重跑健康度 → 該列 badge 即時更新（⚠️→🔴）。
- **重拍整包覆蓋**手動編輯（不 sticky，定案）；無路徑驗證/無 `~` 展開。
- codex 兩輪（標準+對抗性三視角）抓修 2：H1 cwd 編輯在 busyRef 單飛守衛外 → 與 in-flight capture/restore race + lost update（改 `disabled={busy}` 不進編輯 + commit busy 時 reject）、H2 Enter 在 IME composition 中誤 commit（compositionRef + isComposing 雙偵測，composing 時 Enter/Esc 不觸發——CJK 選字安全）。subagent-driven TDD；settings+snapshot 314 測試綠 / 全套 3963 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.323] - 2026-07-14

### Style(new-tab): Sessions 列表標題亮度對齊 id (#929)

New-Tab Sessions 列表的 session 動態標題（`pane_title`）由 `text-text-muted`（較暗）改為 `text-text-secondary`，與 session code（id）同亮度。純 SPA→HMR。

## [1.0.0-alpha.322] - 2026-07-14

### Feat(new-tab): Sessions 列表在 session id 後顯示動態標題 (#925)

New-Tab 的 Sessions 列表，每列在 **session code 之後**顯示該 session 的動態標題（`pane_title`，即 tab 上顯示的那種活動標題），填入原本空白區域。

- `SessionRow` 由 `[icon] name  code(ml-auto)` 改為 `[icon] name  code  {pane_title}`；code 去掉 `ml-auto`、`pane_title` 接於其後、`text-text-muted` `truncate` 填滿剩餘空間；僅 `session.pane_title` 存在時渲染，不 gate on `dynamicTabName`。
- Flex 佈局經 codex 4 輪 review + headless-chromium 實測收斂：name `truncate` 可截斷（防長名溢出）；**僅在有 pane_title 時**加 `max-w-[50%]` cap（保 title 可見、避免兩者皆長時 title 塌成 0px）；無 title 時 name 用全寬。code 恆 `flex-shrink-0`。
- vitest 3930 綠 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.321] - 2026-07-14

### Fix(snapshot): 擷取 cwd 改用 pane_current_path，非 session 起始目錄（`~`）(#926)

Workspace Snapshot 實測發現 Sessions 對帳表的 Directory 欄大多顯示 `~`。根因：`capture.ts` 取 `listSessions().cwd`（= tmux `#{session_path}` = session 起始目錄，且不展開 `~`），非目前 pane 的實際 cwd。影響 UI 可讀性，且「Rebuild all」會把 session 重建到起始目錄（常是 home）而非實際工作目錄。

- **修法**：live session 改用 `fetchSessionCwd`（daemon `/api/sessions/{code}/cwd` → tmux `#{pane_current_path}`）解析目前 pane 的絕對 cwd。每 host 仍只 `listSessions` 一次（liveness + name/current_command/mode）；per-session `fetchSessionCwd` **先按 `(host,code)` 去重**再 `Promise.all` 並發、各自 try/catch 隔離。
- **fallback 可觀測**：`fetchSessionCwd` throw/空 → 退回 `listSessions().cwd` 並標 `captureError:'cwd-probe-failed'`（不再靜默偽裝成精準捕獲；non-empty 仍 restorable 保留 best-effort 重建）；兩者皆空 → restorable:false。dead / host-offline 分支不變。
- codex 兩輪（標準 + 對抗性三視角）抓修 2：G1 同 session 多 pane 未去重的並發 `/cwd` race（last-writer 覆蓋真實 cwd）、G2 靜默 fallback 偽裝退化快照。subagent-driven TDD；`src/lib/snapshot/` 72 測試綠 / 全套 3931 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.320] - 2026-07-14

### Feat(new-tab): Sessions 列表 tab 同款 agent 指示器 + 依 host 建立 session 並 attach (#923)

New-Tab 起始畫面的 **Sessions 列表**兩項增強：

- **① tab 同款 icon/status prefix**：每列改用 `<TabIcon>` 顯示與分頁一致的 agent 圖示（Claude Code / Codex / OpenCode）、執行狀態、subagent dots、unread，並跟隨使用者的 `tabIndicatorStyle` 設定。抽出共用 hook `useSessionAgentIndicator`（`compositeKey`→`useAgentStore` + `getAgentIcon` + `tabIndicatorStyle`），`useTabDisplay` 重構為消費此 hook（單一真相來源、行為等價），`SessionSection` 每列抽 `SessionRow`；無 agent 時 fallback 回終端機圖示。
- **② 依 host 建立 session + attach**：每個 host 標題列（含單 host、含零 session 的 host）加獨立 `+` 建立鈕（header 改 `<div>`、collapse 與 `+` 為分離按鈕、`+` 不觸發收合、收合態點 `+` 會展開）。`NewTabSessionForm`（name/cwd/mode，沿用 `createSession()`）建立成功後直接 `onSelect` attach 進**當前 pane**（全頁 new-tab 或分割格皆適用）。`+`/submit 採 host 頁 offline 語意 disable（`!runtime || status!=='connected' || tmuxState==='unavailable'`）。

多重資料安全守衛：blank code / host-live 送出前後雙重再查、`activeRef` mounted guard（cancel/collapse/host-removed/tab-switch/StrictMode double-invoke 皆不誤 attach 或 unmounted setState）、`creatingRef` 雙擊防護。

spec + plan 各經 codex 跨模型審一輪並強化；subagent-driven TDD（2 phase 5 task）；PR 經 codex **4 輪** review（標準 + 對抗性三視角 + 2 確認輪）抓修 4 個問題（收合表單失效 / stale async attach / offline pre-POST / StrictMode activeRef）全數修復並附回歸測試。vitest 3927 綠 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.319] - 2026-07-14

### Feat(snapshot): Settings「Snapshot」section UI（Phase 3）(#920)

工作區快照 / 一鍵重建的**使用者入口**：Settings 新增「Snapshot」section，接上 alpha.318 的 headless capture/storage/restore 引擎。至此整個 Workspace Snapshot 功能（Phase 1+2+3）完整可用——拍下工作區、伺服器重開機 / tmux 重啟後依 name+cwd 一鍵重建。

- **健康度四態對帳表**（掛載時各 host `listSessions` 即時對帳）：🟢 活（live 含 code+name，與引擎 reattach 規則一致）/ 🔴 已死可重建（restorable 且有 cwd 且 host 可達）/ ⚠️ 只保結構（不可重建）/ ⚪ host 離線。Tmux 區塊對帳表（host/name/cwd/current_command/健康度）+ Tabs 區塊樹（workspace→tab→pane）。
- **三動作 + 復原**：拍快照、重建所有 session、還原 tab 佈局、全部還原、復原上次還原（無 `-prev` 時 disabled）。`useRef` 單飛守衛；toast 依 `RestoreReport` 彙總；restore 失敗以 error tone + `rebuiltButUnattached` 揭露。`SETTINGS_ORDER.SNAPSHOT=22` + 38 個 i18n key（en/zh-TW 對等）。
- **codex 兩輪跨模型審**（標準 + 對抗性 3-parallel 攻擊/防守/體質）抓修 3 項：capture 後 `snap` mount 凍結不刷新致同頁 capture-then-restore 失效（改可變 state + mutation 後 refresh 重跑健康度對帳）、健康度 🔴 predicate 未含 `cwd` 與引擎 rebuild 規則不一致、`RestoreError` 失敗被降級成 warn（改 error tone + 保留揭露）。元件/測試拆 hook 重構延後 #921。
- subagent-driven TDD；`SnapshotSettingsSection` 19 測試綠 / 全套 3910 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.318] - 2026-07-14

### Feat(snapshot): 工作區快照 capture／storage／restore 引擎（Phase 1+2，headless）(#914)

工作區快照 / 一鍵重建的**純前端邏輯層**：拍下整個工作區（workspace/tab/pane 結構 + 每個 tmux session 的 name/cwd），讓伺服器重開機 / tmux 重啟後依 name+cwd 一鍵把工作環境重建回來。本版僅引擎，Settings UI 為後續 Phase 3。Spec/Plan 經 codex R1–R3 全過。

- **Phase 1 資料模型 + capture + 持久化**：`types.ts`（`SessionMeta`/`WorkspaceSnapshot`/複合鍵 `Remap`/`RestoreReport`/`RestoreError`）、`storage.ts`（正本 + `-prev` 後悔藥 key，走 `browserStorage`）、`capture.ts`（`buildSnapshot` 純建物件不寫 storage、`captureSnapshot` 寫正本 + 統計；每 host 一次 `listSessions`，live 無 cwd / 已死 / host 不可達 → `restorable=false`）。
- **Phase 2 restore 引擎 + 三動作**（`restore.ts`）：`ensureSessions`（對帳 + 重建，逐筆失敗隔離、以 `createSession` 回傳物件為準）、`remapLayoutSessions`（純函式改寫 layout 樹）、`validateSnapshotConsistency`（5 條導航守衛）、`replaceTabSnapshot`（整包取代兩 store，任一步 throw 全回滾）、三動作 orchestration（`rebuildAllSessions` 不收窄含 orphan、`restoreTabLayout`、`restoreAll`、`undoLastRestore`）、`syncSessionStore` per-host 聚合。
- **實作中修正 plan 矛盾**：`-prev` 誤用 `captureSnapshot`（會覆寫正本毀還原來源）改用 `buildSnapshot`（B1）。
- **codex 兩輪跨模型審**（標準 + 對抗性 3-parallel 攻擊/防守/體質）抓修 5 項：syncSessionStore 整包覆蓋抹掉非快照 live session（+ 修法引入的幽靈 session）、`writePrevSnapshot` 失敗未揭露已建 session、reattach 只憑 code → 改 **code+name 都吻合**（防 daemon 重啟 code 重用錯接）、storage 只驗 version → 加輕量 shape 守衛。駁回「pane mode 不同步」（誤報，違 app 慣例）。restore.ts 拆檔延後 #918。
- subagent-driven TDD；`src/lib/snapshot/` 68 測試綠 / 全套 3889 / lint / build。純 SPA→HMR。

## [1.0.0-alpha.317] - 2026-07-14

### Fix(panes): 分割後 pane 內容無法捲動 — split 容器改用 h-full 取得有界高 (#916)

分割（pane split）後 pane 內容無法捲動（如 new-tab 起始畫面在半高分割格搆不到底部）；全頁單葉分頁不受影響。

- **根因**：頂層 split 直接掛在 `TabContent` 的 `<div class="absolute" style="inset:0">` **block** 容器下，而 split renderer 的 root 用 `flex-1`——flex-item 屬性在 block 父層失效 → split 容器塌陷成 content height → 往下每層 pane 失去有界高 → pane 內 `overflow-y-auto` 撐到全內容高、永不可捲，尾端被上層 `overflow-hidden` 裁掉。單葉全頁分頁走 `h-full w-full`（有界）故正常，形成「全頁可捲、分割不可捲」的差異。
- **修法**：split 容器 root 由 `flex-1 flex …` 改為 `h-full w-full flex …`，對 absolute wrapper 的有界高解析成功並級聯穿透巢狀 split（其父為 flex wrapper，100% 同樣可解析），`w-full` 維持橫向分割滿寬。
- 新增結構守衛測試（split root 必含 `h-full w-full`、不含 `flex-1`）。jsdom 不量佈局，另以 headless-chromium 佈局實測確認頂層與巢狀 split 皆從 `canScroll:false` → `canScroll:true`、last item 可達。全套 vitest 3821 綠 / lint / build。codex 兩輪（標準+對抗性三視角）皆 approve、無實質發現。純 SPA→HMR。

## [1.0.0-alpha.316] - 2026-07-14

### Fix(new-tab): 「Bring in an open tab」僅在分割格顯示，不再外洩到全頁新分頁 (#913)

「Bring in an open tab」區塊（alpha.315 #910 引入）原本會出現在**每個獨立的全頁新分頁**。根因：全頁新分頁本身就是「1 tab + 1 pane」，一律帶著 `currentTabId + currentPaneId`，而這是唯一的顯示 gate。此區塊只有在 new-tab pane 是**分割（split）的其中一格**時才有意義（把另一個 tab 的內容拉進這格對照檢視）。

- `NewTabPage` 的 `bringInCandidates` 新增 gate：僅當擁有此 pane 的 tab 為分割狀態（`countLeaves(tab.layout) > 1`）時才列出候選。全頁新分頁回到原本裸啟動畫面（`h-full` 捲動契約不變），區塊只在 split 一格時重新出現。
- 測試：新增「全頁新分頁即使有多個 movable tab 也不渲染區塊」；既有 bring-in 測試改以 `splitPaneBlank` 建構真實分割反映新語意；保留 current tab 不列入自身候選的驗證。全套 vitest 3820 綠 / lint / build。codex 兩輪跨模型審（標準 + 對抗性三視角）皆 approve、無實質發現。

## [1.0.0-alpha.315] - 2026-07-11

### Feat(panes): 跨 workspace 把已開分頁拉進分割格 (#910)

pane split UI 的 Phase 3（承接 alpha.313 PR-A 的右鍵分割 + StatusBar 分割鈕）：在空白 New Tab 分割格可**把已開的分頁（跨 workspace）拉進來**做雙邊檢視。

- **`moveTabContentIntoPane` 搬移原語**（`lib/pane-move.ts`）：guard（來源存在 / `!locked` / `countLeaves===1` / kind ∈ `MOVABLE_KINDS` / 非自身 / **target tab+pane 存在**）後 `setPaneContent` 注入目標格，再經 `closeTabInWorkspace({skipHistory:true})` 退場。刻意走搬移語意（非 `tab-lifecycle::closeTab` 銷毀語意）→ 跳過誤報 dirty-confirm、內建 active fallback。allowlist = `editor / tmux-session / image-preview / pdf-preview`（排除 browser 的 BrowserView 生命週期）。
- **`NewTabPage`「Bring in an open tab」區塊**：跨所有 workspace 列「單-pane 且 primary kind ∈ allowlist 且 `!locked` 且非本 tab」候選，標所屬 workspace 名；點擊即搬入。`NewTabPaneWrapper` render 時反查 `currentTabId` 傳入；無候選時回裸 grid 保護既有 h-full 捲動契約。
- codex 三輪跨模型審抓出並修復三個資料安全問題：editor 未存 buffer 於 pull-in 時因 unmount `closePane` 早於 mount `attachPane` 遺失（pre-bind 目標 pane 引用）、跨 host 相同 sessionCode 誤標名稱（改用 `hostId` scope lookup）、stale target 時內容遺失（加 target 存在性 guard）。subagent-driven TDD；全套 vitest 3808 綠 / lint / build。

### Fix(terminal-link): 檔名點分段允許 `+` build metadata (#911)

終端機檔案連結偵測在語義版本 build metadata 的 `+` 處截斷（`com.wake.custom-css-0.0.0+075a408.tar.gz` 只抓到 `...0.0.0`）。四條路徑 regex（ABS/TILDE/REL/BARE）的副檔名鏈由 `(?:-[A-Za-z0-9]+)*` 擴為 `(?:[-+][A-Za-z0-9]+)*`，讓 `+` 比照連字號納入合法字元（承接 alpha.314 #904 連字號修正的同家族延伸）。

版本字串排除採**明確 bias — 不 linkify 版本雜訊**：從第一個 `+` 切開，前段為純版本號者不當連結（`1.0.0+exp.sha`、`1.0.0+build-123` 等終端常見版本輸出）。真實 build 產物帶套件名 stem（`com.wake.custom-css-...`）故前段非純版本、仍可點。取捨：stem 本身為裸版本號的檔名（`v1.0.0+build123.txt`）不連結——罕見，優於把大量版本雜訊變成死連結。此邊界（`.sha` build 識別碼 vs `.log` 真副檔名語法無法區分）經 codex 三輪確認為物理互斥，由使用者定案 bias。TDD；全套 3795 綠。

## [1.0.0-alpha.314] - 2026-07-08

### Feat(editor): 新分頁「最近開啟」檔案清單 (#905)

在 Editor 新分頁區塊、New File 按鈕下方加入**最近開啟檔案清單**，涵蓋本機 / 遠端(daemon) / in-app 檔案。

- **資料層** `useRecentFilesStore`（persist localStorage，仿 browser history）：entry = `{source, path, name, kind, openedAt}`，以 `source(+host)+path` 去重、most-recent-first、cap 50。
- **記錄規則**：現有檔**開啟即記**（`defaultTabOpener`、missing-file popup `onOpenPath`、`openInAppFile`）；新建檔**存檔才記**（`EditorPane.handleSave` 一般存檔分支 + `saveUntitledBuffer`）；從清單重開亦刷新 recency。不攔 tab restore 避免污染。
- **UI**：固定 chips（全部 / 文字 / 圖片 / PDF）；每列 kind 圖示 + 檔名（截斷 + tooltip）+ 淡路徑 + daemon host badge。
- **點擊**：best-effort 就地開（沿用 `onSelect`，與 New File 一致）。daemon 先驗 host 存在（擋 `getDaemonBase` fallback 開錯主機）+ stat（含 isFile 守衛）；成功開、找不到 / 非檔案 / 錯誤跳 toast。

設計經 brainstorming 定案、spec+plan 各過一輪 codex 跨模型審並整合缺口；實作以 subagent-driven TDD 分 5 task（store→recorder→wiring→openRecentEntry→UI）；PR 經 codex 兩輪（R1 修 reopen 未刷新 recency；R2 三視角指出 daemon pane host-blind / read 失敗開空 buffer / persist 無 guard 皆為既有全 app 行為，追蹤 #906/#907/#908）。全套 vitest 3772 綠、lint / build 通過。

### Fix(terminal-link): 檔名點分段允許連字號 (#904)

終端機 pane 的檔案連結自動偵測在檔名**點分段含連字號**時會截斷（如 `docs/souls/morphy.pre-edit.SOUL.md` 只抓到 `docs/souls/morphy.pre`）。四條路徑 regex（ABS/TILDE/REL/BARE）的副檔名鏈由 `(?:\.[A-Za-z0-9]+)+` 改為 `(?:\.[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*)+`——允許段內以單一連字號連接的英數群組（`pre-edit`、`min-2`、`2024-01`），且不吃前導 / 結尾 `-`。

同步強化版本字串排除（`allExtensionsVersionLike`）：`v1.2.3-beta` 等 semver prerelease 不再被誤判成假連結，`bar.min-2.js` / `v1.2.3.tar.gz` 等真檔名仍連結。純數字-prerelease 單一副檔名（`report.2024-01`）維持既有 IP/版本取捨不連結。TDD；全套 3756 綠。

## [1.0.0-alpha.313] - 2026-07-08

### Feat(panes): pane split UI — 右鍵選單 + StatusBar 分割鈕 + 移除 grid-4 (#901)

為既有 tab 分割引擎補**互動式 UI 入口**（引擎原已支援任意巢狀，僅缺入口）：

- **Pane 右鍵選單**（非 editor pane）：`Split Horizontal` / `Split Vertical`（恆有）＋ `Close pane` / `Detach to tab`（僅 `countLeaves>1` 時）。editor(Monaco) pane **不攔截右鍵**（保留原生選單）；攔截型 pane 的 **`Shift`+右鍵** 為 escape hatch 放行原生選單（xterm/browser）。
- **StatusBar 分割鈕**：終端機 pane 在 `terminal/stream` 膠囊旁加 split-H/V 鈕，作用於 primary pane。
- **`splitPaneBlank`**：新 store action，分割切出空白 New Tab 格（不改既有 `splitPane`）。
- **移除 grid-4 硬編碼特例**：田字版型的連動分隔線特例移除，grid-4 形狀改由通用遞迴 renderer 渲染、各 split 獨立 resize，與任意巢狀相容。TitleBar 版型鈕保留 single / split-h / split-v。

設計經 brainstorming 定案、spec+plan 各過 codex 審；PR 經 codex 兩輪 + 最終確認 review（R1 修單-pane wrapper flex→block 縮寬回歸；R2 修選單 stale 誤關整個 tab + grid-4 內 split 連動 resize 衝突；final 抓出 browser pane 右鍵入口受 BrowserView overlay 阻擋 → 追蹤 #902）。

**純 SPA → HMR 即時生效。** 已知限制：browser pane 右鍵分割需 Electron IPC（追蹤 #902）。剩 PR-B（跨 workspace 拉 tab）待做。

## [1.0.0-alpha.312] - 2026-07-03

### Feat(editor): Outline-style markdown Live Mode + content-width toggle (#899)

markdown「Live Mode」（Tiptap WYSIWYG）套用 **Outline（getoutline.com，同屬 ProseMirror 家族）** 的閱讀排版，解決先前 `max-w-none` 貼齊面板、寬螢幕單行字元數暴增（100+）閱讀不適的問題。樣式數值直接取自 Outline 原始碼：內容欄 `52em`、字級 h1 28 / h2 22 / h3 18 / h4 16 / h5-6 15、行高 `1.7`（沿用 Outline 對複雜文字的放鬆，含 CJK）、標題無底線 600 粗、code `6px` 圓角。

新增**內容寬度切換**（全域偏好、持久化）：`narrow`（限寬置中 52em，預設）⇄ `full`（滿寬）；toggle 位於狀態列 Live Mode 鈕旁，**僅 Live Mode 顯示**。

- 樣式 scope 於 `.tiptap-editor`，**顏色全用既有 theme CSS 變數**（跨主題自適應，不改配色）；不影響 `MessageBubble` 等泛用 prose 使用處。
- 系統性中和 Tailwind Typography 外洩的 prose 預設：blockquote 斜體 + open/close-quote 彎引號、inline code 注入的 backtick + 600 粗細（掃過 built CSS 全部 4 條 pseudo-content 規則確認無殘留）。
- 寬度偏好放 `useEditorSettingsStore`（persist + sanitize + rehydrate 信任邊界）；寬度改由 `<EditorContent>` 外層置中欄 wrapper（`max-w-[52em] mx-auto box-border`）控制，`useEditor` 初始化不變，避免 remount / viewState 風險。
- Spec + Plan 各經 codex 跨模型審閱定稿；PR 經 codex 兩輪深度 review（R1 blockquote pseudo 外洩 / 排除 h2 padding 誤報；R2 Diff view toggle 洩漏 AC4 + inline code backtick）全數修正。

**純 SPA → HMR 即時生效。** 取捨：`52em` 先忠實沿用 Outline 值，CJK 長文觀感可後續微調。

## [1.0.0-alpha.311] - 2026-07-01

### Fix(spa): keep light (non-terminal) tabs alive across switches (#897)

修正 **Live Mode（及任何 editor tab）切走再切回會「重新載入」回到頂端**。根因：`keepAliveCount` 預設 0，alive pool 只保留 active tab → editor 每次切 tab 都 unmount+remount → viewState 還原不穩 → 回頂 + reload flash。而 `keepAliveCount` 本質是 terminal renderer（xterm/WebGL）記憶體設定，editor 被連坐 evict。

修法：把 tab 分 **light vs heavy**。heavy = 含 `tmux-session` / `browser` pane（GPU/WebContents 記憶體）；light = 自足、一次性載入、不做背景工作的渲染器（allowlist：`editor` / `image-preview` / `pdf-preview` / `dashboard` / `history`）。light tab 不受 keepAliveCount 連坐，以獨立 LRU（`LIGHT_KEEP_ALIVE_MAX = 8`，most-recent-first）保活 → 工作集切 tab 不 remount、scroll/mode 原生保留、記憶體有界。heavy tab 維持原 keepAliveCount LRU（無 light tab 時 alive set byte-identical）。

- 新 `isLightTab(layout)`（`lib/pane-weight.ts`）用 light allowlist 掃 leaves（未知/未來 kind 預設 heavy，保守）。
- `useTabAlivePool` 加 optional `light`：bounded light LRU + budget-aware history trim（不因大量 heavy 訪問而遺忘仍在預算內的 light tab）+ pinned light/heavy 皆豁免預算 + active 恆存活。
- PR 經 codex 6 輪 adversarial review 逐一關閉：memory-monitor/settings/new-tab 誤分類（背景輪詢/可擴充 host）、light 無上限記憶體、history trim invariant、pinned light 契約——最終 approve。

**純 SPA → HMR 即時生效。** 取捨：最多 8 個近期 light tab 常駐（editor/Monaco/Tiptap，遠小於 terminal GPU）；terminal 記憶體行為完全不變。

## [1.0.0-alpha.310] - 2026-07-01

Batch A — editor markdown + terminal-link 使用者需求。

### Feat(spa): markdown opens in Live Mode + fix Live Mode scroll jump on tab switch (#894)

- **markdown 預設 Live Mode**：`.md` 從 raw（Monaco）改為預設進 **Live Mode（Tiptap wysiwyg）**；非 markdown 維持 raw。`EditorPaneState.editorMode` 改為 `EditorMode | null`（null = 未明確選擇 → render 時解析語言預設），使用者明確切換的具體值優先且跨 remount 保留。語言預設只在 paneState 對齊 buffer 後套用，保住 #863 不變式（Tiptap lazy 不對 stale 掛載、buffer 切換不閃 Loading）。
- **修 Live Mode 切 tab 跳頂**：`isActive` false→true 的重新聚焦改用 `.focus({ preventScroll: true })`，避免把游標（頂端）捲進視野造成整頁跳頂。
- （探索過的「明確 mode 選擇跨 pane 換檔持久化」因 stale-path meta-drift 撤回；切 tab 的 mode 保持在 keep-alive 下本就 work。）

### Feat: shift+click a URL opens the OS default browser, not a mini window (#895)

終端機（及 browser pane 內）URL 的 shift+click 從「Electron 迷你視窗」改為「**OS 預設瀏覽器**」（一般點擊仍開 app 內 tab）。SPA url opener 的 `openMiniWindow` dep 換成 `openExternal`，接到新的 `openExternalUrl` bridge → main 端 `shell:open-external` handler，經 `isAllowedUrl`（`new URL().protocol` 對 http/https 白名單）scheme 防護後 `shell.openExternal`。統一 `openAllowedExternalUrl` helper try/catch 避免 unhandled rejection。迷你視窗保留給 browser pane pop-out。**⚠️ 含 Electron main/preload 改動，Air 端需重打包 + dev update 才生效。**

## [1.0.0-alpha.309] - 2026-06-30

### Fix(spa): new-tab / history panes fill height via h-full, not flex-1 (#891)

修正「起始畫面（new-tab）」與「History pane」**無法捲動**的 layout bug——延續 #889 的同一 bug class。

根因：`NewTabPage` / `HistoryPage` root 用 `flex-1` 撐高，但 `TabContent` 的每分頁掛載層是
`position:absolute inset:0` 純 block（非 flex container），`flex-1` 在其下失效 → root 塌成內容高 →
內層 `overflow-y-auto`（new-tab 欄 / history 列表）拿不到有界高度就不能捲。`EditorPane` / `TerminalView`
本來就用 `h-full w-full` 故正常。

修法：兩 pane root（NewTabPage 含 grid + empty-state + placeholder 四個 return）`flex-1` → `h-full w-full`。
`HostPage` / `SettingsPage` root 是 `flex h-full`、scroll 容器為正確 flex child，**不受影響、未動**。回歸測試
斷言兩 root 用 `h-full` 且非 `flex-1`；vitest 3696 綠、lint、build 通過，codex review 0 findings。

## [1.0.0-alpha.308] - 2026-06-30

### Fix(spa): image/pdf preview fill height via h-full, not flex-1 (#889)

修正在 tab 內瀏覽圖片時「寬度等比縮、高度卻不縮、且無法捲動」的 layout bug（PDF 預覽同症狀）。

根因：`ImagePreviewPane` / `PdfPreviewPane` root 用 `flex-1` 撐高，但 `TabContent` 的每分頁掛載
層是 `position:absolute inset:0` 的純 block（非 flex container），`flex-1` 在其下失效 → root 塌成
內容高 → 內層容器無明確高度 → `<img>` 的 `max-height:100%`（PDF 為 iframe 的 `h-full`）解析成
`none`，只剩寬度受限 → 直幅圖/PDF 高度溢出被 `overflow-hidden` 裁掉、不可捲。split / 有 header 的
情境父層是 flex 容器故不受影響，僅「單一整頁」會中。

修法：兩個 pane root `flex-1` → `h-full w-full`，對齊既有的 `EditorPane`（`h-full` 能解析到 block
父層的 inset:0 明確高度）。恢復 `object-contain` 長邊優先等比縮，並讓 actual 模式的捲動路徑可達。
回歸測試斷言兩 pane root 用 `h-full` 且非 `flex-1`；vitest 3694 綠、lint、build 通過，codex review 0 findings。

## [1.0.0-alpha.307] - 2026-06-29

### Feat(storage): restore UI — history/viewer/restore + pane reconciliation (Phase 2c-2) (#887)

子系統 2 Phase 2c-2：前端還原 UI，把已 ship 的 2c-1 還原引擎接到 Storage pane 右側欄 + modal。
**Storage 子系統 2（daemon 備份/還原版本庫）全系列（2a/2b/2c）完成。** spec §4.4/§4.7 + AC-2c。
4 TDD task；PR 經 codex 標準 + 三視角 + **5 輪深度 review**，逐層關閉「跨 host UI 狀態」整類問題
（2 P2 + 3 high + 2 medium 全修），full suite 3692 綠、lint、build 通過。

- **history list**（`BackupHistoryList`，右側欄）：`getHistory` newest-first + device/相對時間/trigger/
  **fork badge**；loading/error/empty；fetch deps `[hostId, lastBackupAt]`；rows 以 host tag 標記，
  切 host 同步隱藏舊主機的列（不可誤點）。
- **manifest viewer modal**（`BackupSnapshotModal`）：點 row 開 modal，`getSnapshot` 列檔案清單
  （path/kind/size/words，**不下載 blob**）+ header（device/time/trigger/fork）+ Close/Restore。
- **restore wiring**（`restore-wiring.runRestore`）：綁 live deps（dirty/locked guard、pre-restore
  forcePost、read API、backend）；done 後跑 pane reconciliation；backing-up 禁用；blocked 只列衝突
  中止（不隱式 save/discard）；pre-commit 失敗才報 restore error（樹未動），post-commit reconcile
  best-effort 不 throw。
- **pane reconciliation**（`reconcile-panes` + `pane-tree.remountLeaf` + `useTabStore.remountPane`）：
  還原後對齊開啟的 In-App pane —— close removed editor / reload changed clean editor
  （savedContent+isDirty+lastStat）/ preview **force-remount**（換 leaf pane.id 強制 re-read）；
  file→dir 轉換視為 close；failure-isolated。
- **跨 host 安全**：selection 綁 host、restore outcome 以 request token request-scoped、success
  banner host-scoped —— 任何 host 切換都不會用錯 host 還原或把 A 的狀態顯示在 B。

## [1.0.0-alpha.306] - 2026-06-29

### Feat(storage): restore engine — Phase 2c-1 (subsystem 2) (#885)

子系統 2 Phase 2c-1：前端還原引擎（headless，無 UI；由後續 2c-2 接上 history/viewer/restore UI +
pane reconciliation）。spec §4.4 + AC-2c。plan 經 codex 2 輪 review（R1 1C+2P1+2P2+1P3 → 全修 →
R2 READY-TO-IMPLEMENT）；5 個 TDD task 派 subagent 實作；PR 經 codex 標準 + 三視角 + 2 確認輪
（共修 1 P1 + 1 Critical + 1 medium，最終 R4 approve）。full suite 3651 綠、lint、build 通過。

- **讀取 client**（`backup-api`）：`getHistory`/`getSnapshot`/`getBlob` + `SnapshotSummary`/
  `SnapshotDetail` 型別；404 用 typed `BackupNotFoundError`。
- **`SupportsReplaceTree` capability + guard**（鏡像 `SupportsUniqueCreate`）；`InAppBackend.replaceTree`：
  單一 IDB txn 清空 root 子樹 → dirs-first → files，前置 relPath 全驗證，失敗 abort 回滾，不 emit
  `onMutation`（restore 不自觸發 auto-backup）。
- **原子還原守衛**：`InAppBackend` 維護 monotonic tree revision（每個 mutation txn 內 bump）；
  `replaceTree(…, expectedRevision)` 在自己 txn 內、刪除前重驗 revision，不符即 `TreeRevisionMismatchError`
  不 mutate —— 消除「pre-restore 後到 apply 前的本機寫入被靜默覆寫」的資料遺失窗口。
- **`backupNow(hostId, {trigger?,forcePost?})`** 回傳 `snapshotId|null`；`forcePost` 繞過 client no-op
  （pre-restore 一定 POST）；per-host single-flight（restore 與 auto-backup 不重疊）。
- **`restoreSnapshot(deps)`** orchestrator：guard → capture revision → pre-restore（forcePost）→
  getSnapshot + 逐 blob 驗 sha256/size（per-entry）→ apply-time guard re-check → atomic
  `replaceTree(expectedRevision)`。任一失敗在任何 IDB 寫入前 abort（R2-Pa）。
- **`findRestoreConflicts`** pure guard：dirty inapp buffer / locked tab 含 inapp editor pane（R2-Pg）。

### 不在本階段
2c-2：history sidebar list + manifest modal + Restore 按鈕 wiring + pane reconciliation（含
`remountPane` primitive）+ fork 指示。

## [1.0.0-alpha.305] - 2026-06-29

### Feat(storage+browser): post-2b UI polish (#883)

使用者實測 Phase 2b 後的 4 項 UI/UX polish（輕量 self-review；vitest 3616 / lint / build 全綠）。

- **調亮文字**：Storage 檔名 `text-text-secondary`→`primary`、size/words metric `muted`→`secondary`；
  備份側欄面板 `muted`→`secondary`、標題 →`primary`。
- **「New Buffer」→「New File」**：`editor.buffers.new`（en `New File` / zh-TW `新增檔案`），對齊
  Editor 區措辭。
- **隱藏 URL 歷史 dropdown（邏輯保留）**：`BrowserNewTabSection` 加 `SHOW_URL_HISTORY_DROPDOWN`
  flag gate render，history store / 鍵盤導航 / outside-click / scroll-into-view 全留，翻 flag 即還原。
- **啟動時檢查一次 backup**：backup auto-trigger 加 `runInitialCheck`，app boot 時 host 就緒即跑一次
  `backupNow`（未就緒則等首個 host 出現時觸發一次，idempotent，dispose 清乾淨）；靠 no-op 抑制 +
  daemon content-keyed no-op，內容沒變只是輕量 round-trip。

## [1.0.0-alpha.304] - 2026-06-29

### Feat(storage): front-end backup engine — Phase 2b (subsystem 2) (#881)

子系統 2 第二階段：前端 `spa/` 備份引擎，驅動已 ship 的 2a daemon backup API。把 Storage pane
右側欄的「備份（即將推出）」placeholder 填成實際備份引擎：監聽 In-App（IndexedDB `/buffer`）變更
→ debounce ~2s → walk 整棵樹 → 瀏覽器 sha256 → per-blob negotiation 上傳 → POST snapshot →
右側欄狀態。Plan 經 codex 3 輪 review（R3 READY-TO-IMPLEMENT，R1 1C+4P1+4P2+2P3 / R2 4 findings
全收斂），7 個 vertical-slice TDD task 派 subagent；full suite **322 files / 3612 tests** 全綠、
lint clean、build OK。

- **新 util**：`lib/crypto-hash.ts`（WebCrypto sha256，對齊 daemon lowercase hex）、`lib/text-metrics.ts`
  （從 `StorageRow` 抽出 word-count SOT，row 與 manifest builder 算出同一指標，無回歸）。
- **manifest builder**（`lib/storage-backup/manifest.ts`）：walk tree、**UTF-8 byte comparator**
  canonical sort（配 daemon Go byte order，否則 `400 unsorted`）、空 dir 進 manifest、blob dedup。
- **API client**（`backup-api.ts`）：`postMissing`/`putBlob`/`postSnapshot` over `hostFetch`，
  錯誤冒泡 HTTP status 供 engine 區分 409/400/413。
- **engine + `useBackupStore`**（per-host keyed）：client no-op suppression、任何成功 post（含
  server no-op 回 head）皆收斂 lineage、`parentId`=自身 prior（await 前同步捕捉，Design 5）、
  `applyRemoteBackupDone` status-only 不污染 lineage、same-bytes rename（0 PUT/1 POST）。
- **`backup:done`** 加進 `HostEvent` union + `useMultiHostEventWs` dispatch（只跨裝置 refresh，
  own-device 忽略）；`backup-ws-dispatch.ts` 可單測 seam。
- **右側欄狀態面板**（`BackupStatusSidebar.tsx`）：上次備份/備份中/inline error/尚未備份；i18n en+zh-TW。
- **In-App mutation emitter + 常駐 auto-trigger**：`InAppBackend.onMutation`（write/delete/mkdir/
  rename + unique-create，**非** replaceTree）；掛 app bootstrap（`main.tsx`，**非** StoragePane，
  Storage pane 未開也備份）；debounce capture mutation-time host、host 變更 cancel-only never retarget。

### 不在本階段
2c（前端還原 UI：history/viewer、dirty-block guard、atomic `replaceTree`、pane reconciliation、
fork 顯示）後續 PR。

## [1.0.0-alpha.303] - 2026-06-29

### Feat(storage): daemon backup/restore snapshot store — Phase 2a (subsystem 2) (#879)

子系統 2 第一階段：daemon 端 content-addressed append-only snapshot 版本庫，為前端 In-App
（IndexedDB `/buffer`）檔案樹提供跨裝置備份/還原的後端。純 Go daemon，無前端改動。Spec 經
4 輪 codex review（13→7→4→0 findings）收斂 SHIP-READY，plan 經 3 輪 review READY-TO-IMPLEMENT；
7 個 vertical-slice TDD task 派 subagent；PR 兩輪深度 review（標準 + 攻擊/防守/體質）+ 兩輪確認
共 1 P1 + 5 P2 + 2 P3 findings 全修（1 path-encoding 誤報排除），full suite 全綠。

- **新 module** `internal/module/backup/`（比照 sync 四層）+ 獨立 `backup.db`（無 FK，references
  在 handler 驗證，避開 #850 DSN-pragma 坑）：`backup_blobs`（sha256 去重、immutable）+
  `backup_snapshots`（append-only，parent_id DAG）。
- **API** `/api/backup/`：`POST /missing`（per-blob negotiation）、`PUT/GET /blob/{hash}`（raw
  blob，cap+1→413、sha256 驗證、冪等、404）、`POST /snapshot`（單一 `BEGIN IMMEDIATE` txn：
  read head→validate→content-keyed no-op→fork→insert→GC）、`GET /history`、`GET /snapshot/{id}`。
- **正確性保證**：content-keyed no-op（manifest==head 即不寫列，與 parentId 無關，跨裝置不產生
  重複 fork）；`is_fork` 僅 content differs 時評估；GC union keep-set（latest 100 ∪ 90 天 ∪
  ancestor closure，非 hard cap）+ blob refcount/grace；manifest canonical order + well-formed
  tree 驗證；`backup:done` 廣播僅 committed write（no-op 不廣播）。
- **DoS 防線**：snapshot/missing body streaming decode（item cap+1 即 413，不先 materialize）+
  `MaxBytesReader` 硬上限 + 外層 object 收尾/EOF 驗證（拒 truncated/trailing）。
- **體質**：store 拆 `blob_store`/`snapshot_store`/`query_store`/`gc` 分檔；serialisation 以
  deterministic `afterReadHead` barrier 測（broken 非交易實作穩定失敗）。

### 不在本階段
2b（前端備份引擎）、2c（前端還原 UI + fork 顯示）各自後續 PR。

## [1.0.0-alpha.302] - 2026-06-28

### Feat(storage): In-App upload / download / binary-disposition / quota (subsystem 1, Phase 1c) (#877)

子系統 1 第三階段：在 1a 巢狀樹 + 1b 巢狀 CRUD 之上補齊檔案進出。純前端（IndexedDB
`InAppBackend`），無 daemon/Electron-IPC 改動。Detailed plan 經 3 輪 codex review 收斂
SHIP-READY；4 個 TDD task 派 subagent；PR 兩輪深度 review（R1 標準 + R2 攻擊/防守/體質）
共 7 findings + 確認 review 補 1 項 defense-in-depth，全修後 full suite 3555 綠。

- **上傳**：toolbar file picker + OS 檔案拖入（native HTML5 drop，用 `dataTransfer.files`
  與 1b dnd-kit node-move 區分共存）；泛化 `createUnique(dir, base, ext: string, content?)`
  以 IDB `store.add()` 原子保留+寫入（不覆蓋；suffix `-N`）；**file.name sanitise**（展平
  basename、去控制字元、拒 `.`/`..`/路徑分隔符、`isUnderRoot` + traversal 段防線）防 path
  traversal/隱藏資料；`uploadFiles → UploadSummary{uploaded[], failed[]}` typed kind 回報。
- **下載/匯出**：單檔 `backend.read`→Blob→anchor download（byte-identical）；抽共用
  `lib/download-file.ts`。
- **非可預覽 binary 開檔→下載 disposition**：`openInAppFile` 對 `DOWNLOAD_EXTS`
  （docx/xlsx/zip…）read+download 不掛 editor；抽 leaf lib `lib/file-extension-roles.ts`
  （`roleForExtension`，registry 與 openInAppFile 單一 SOT）；修 .docx 掛 monaco 變亂碼。
- **軟上限 ~25MB**（write 前擋）+ **quota 錯誤**（`isQuotaError` lift 到 `lib/quota.ts`，
  write catch）→ inline banner warning/error 分流。
- 體質債延後 #872/#873（storage-actions/StoragePane 拆分，1c 已註記加劇）。

## [1.0.0-alpha.301] - 2026-06-28

### Feat(storage): In-App nested CRUD + recursive folder move (subsystem 1, Phase 1b) (#871)

在 1a 巢狀樹之上補齊資料夾級操作。純前端（IndexedDB `InAppBackend`），無 daemon 改動。
Detailed plan 經 3 輪 codex review 收斂 SHIP-READY；依 plan 切 8 個 TDD task 派 subagent 實作；
PR 兩輪深度 review（R1 標準 + R2 攻擊/防守/體質）共 9 findings，fix-wave 全修後確認 review
APPROVE-TO-MERGE。

- **統一 eager 新檔 namer（修 #854 dup-bufferKey race）**：`InAppBackend.createUnique` 用
  IDB `store.add()` 作單一序列化點原子保留檔名；`createUniqueInAppFile(dir, ext)` 收斂三個
  new-file 入口（StoragePane / EditorPane 新 buffer / EditorNewTabSection）。後者由 lazy
  in-memory `untitled:` 改為 eager 立即建檔（`.txt`/`.md` 以 `ext: 'md'|'txt'` 保留），既有
  `untitled:` runtime contract 不動。
- **資料夾可選 + 新增資料夾**：tree selection 從「只選檔案」擴成可選資料夾（單擊 name 選取 /
  caret 獨立展開 / modifier 多選）+ `targetDir` 衍生；toolbar New Folder（`mkdirUnique`
  add-reserve）+ New File 落在選中目錄。
- **改名（檔案 + 資料夾）**：`renameStorageEntry` 統一路徑、**恰好一次** `backend.rename`，
  把舊 `performBufferRename` 重構為 pure `remapPanesUnder`（不碰後端，re-point 含 editor /
  image / pdf 的後代開啟 pane）；碰撞 pre-check + 同名 no-op。
- **遞迴刪除**：locked/dirty 守衛擴成涵蓋資料夾後代（`filePath` 前綴比對）；重疊 target 正規化
  避免半刪；資料夾走遞迴刪除。
- **拖曳搬移**：dnd-kit `DndContext`（每列 droppable 攜帶權威 `targetDir`：資料夾→自身、
  檔案→父目錄、root→`/buffer`）+ pure `moveStorageEntry`（self/descendant/same-parent no-op
  + 碰撞）；保留 click-select / double-click-open / 鍵盤 a11y 共存。
- **體質**：`createUnique`/`mkdirUnique` 改 `SupportsUniqueCreate` capability interface（不再
  強制所有 backend stub）；DnD resolver 抽到獨立 `storage-dnd.ts`。延後體質債 #872-#875。

## [1.0.0-alpha.300] - 2026-06-27

### Feat(storage): In-App nested file manager — "Storage" (subsystem 1, Phase 1a) (#869)

把「Manage buffers」升級為完整的巢狀檔案管理 **Storage**（純前端，後端沿用
`InAppBackend` / IndexedDB，已支援巢狀）。spec + plan 各 3 輪 codex review 收斂後，
依 plan 切 10 個 TDD task 派 subagent 實作；PR 3 輪 review（R1 2 P1 / R2 1 high+2 medium /
R3 2 P2+1 P3）全收斂。

- **修兩個實測症狀**：① 清單裸文字、無法一眼識別 → 巢狀樹 + Phosphor 檔案/資料夾 icon +
  row metadata（size + 文字檔字數，二進位/超 256KB/未知型別只顯 size）；② smart-open 跨 tab
  劫持「開錯/開不了」→ 改走 `openInAppFile`（open-or-focus + registry dispatch：png→圖片
  檢視器 / pdf→PDF 檢視器 / md→編輯器；先 stat 確認存在才開，避免把 stale/已刪檔開成空白
  editor）。
- **巢狀樹**：`listTreeUnder` full-recursive + `useStorageTree`（full-path node identity）；
  展開收合；`STORAGE_ROOT` 集中原本寫死 7+ 處 `/buffer`。
- **元件拆分**：462 行 `EditorBuffersPane` → `storage/` 四模組（StoragePane 兩區框架含右側
  daemon 備份 placeholder / StorageTree / StorageRow / storage-actions，leaf CRUD 改
  full-path、保留 dirty/locked 守衛、rename/delete 同步 image/pdf preview pane）。
- **tab icon** 與 tree row 共用 `fileIconForPath`；breadcrumb popover 巢狀化並走
  `openInAppFile`；改名 Buffers→「Storage」（en + zh-TW「儲存空間」）。

範圍外（後續）：Phase 1b（mkdir / 資料夾搬移 + 後端遞迴 / 拖曳 / 統一 new-file namer #854）、
Phase 1c（上傳/下載 / quota）、子系統 2（daemon 備份還原 snapshot 版本庫，右側欄）。

## [1.0.0-alpha.299] - 2026-06-26

### Fix(sync+test): clear host token as null sentinel + fix stale TabBar tooltip test (#674)

修兩個 origin/main 既有、互相獨立的 baseline 測試失敗（共 4 個 test）。

- **Sync host token 清除契約**：`hostsContributor.deserialize` 在 host 為新增、或 id 撞名但
  endpoint（ip/port）不同時須清除 token（安全：避免 bearer token 重送到不同/惡意 daemon）。
  原寫 `undefined`，改採 **`null`** 明確 sentinel（「已清除，需 re-auth」），`null` 比
  `undefined` 多了跨 JSON persist 邊界存活的語意。`HostConfig.token` 放寬為 `string | null`，
  下游 `string | undefined` sink 於邊界 coalesce `?? undefined` 收斂。抽
  `mergeHostsPreservingTokens` helper 讓 **`full-replace` 與 `field-merge` 兩個 merge mode
  共用同一契約**（原 field-merge 在 conflict 選 remote hosts 時會靜默丟失所有本地 token）。
- **TabBar pinned tooltip stale test**：`HoverTooltip` 已 `createPortal` `role="tooltip"`
  到 `document.body`，test 卻在 tab 子樹內查 → 改用 `screen.getByRole('tooltip', { name })`
  按可及性名稱定位（純 test-only，無 impl 變更）。

## [1.0.0-alpha.298] - 2026-06-26

### Fix(editor): prevent Loading editor flicker on markdown buffer switch (#863)

同一 pane 從一個 markdown buffer（`wysiwyg` 模式）切到另一個 markdown buffer 時，UI
先 paint 一幀 `Loading editor…` fallback（閃爍 + 一瞬焦點中斷）才落到目標 editor。根因是
`attachPane` 為 post-commit `useEffect`：切 buffer 的第一個 render 仍看到舊 buffer 的
stale `paneState`（`editorMode=wysiwyg`、`bufferKey=舊`），stale `editorMode` 驅動
wysiwyg 分支，被 PR #862 的 gating render 擋成 `Loading editor…`。

- **修法**：render 階段同步派生 `alignedPaneState`（僅當 `paneState.bufferKey === key`
  才信任，否則退回 fresh-pane 預設 raw + null viewState）。`attachPane` 對齊後本就會把
  `paneState` 重建為這些預設值，故 stale 視窗直接 render 最終態的 raw Monaco —— 不閃爍，
  也不外洩舊 buffer 的 mode / viewState / cursor / diff 狀態到新 buffer。
- **連帶**：移除 PR #862 的 R3 post-commit gating fallback。wysiwyg 分支前提變為
  `bufferKey === key`，stale 視窗不可能 mount `TiptapEditor`，`didRestoreRef` 不會被
  stale `paneState` 污染（比 R3 更強的保護）。

## [1.0.0-alpha.297] - 2026-06-26

### Feat(editor): focus on switch + markdown wysiwyg scroll/cursor restore (#857)

切換到 editor 分頁時兩個 UX 缺陷：(1) markdown editor 不自動 focus —— focus 只依賴
`[isActive]`，但 editor 的 ready 時機（Monaco async `handleMount` / Tiptap lazy+Suspense）
可能晚於 `isActive` 變 true，`focus()` no-op 且不重試；(2) markdown wysiwyg(Tiptap) 切回
跳到最後 —— Tiptap 完全沒 viewState 持久化，切回 `setContent`+`focusEditable` 把游標拉到
末尾。

- **focus**：Monaco `handleMount` + Tiptap one-shot ready effect 在 editor ready 時若
  `isActiveRef.current` 補 focus（避 stale closure），保留既有 `[isActive]` effect。
- **viewState**：新增 `tiptapViewState` paneState 欄位 + `saveTiptapViewState`；Tiptap
  unmount（`useLayoutEffect` + `didRestoreRef` guard，避免 ref detach 後讀 scrollTop=0 /
  editor-ready commit 後立刻 unmount 漏存）存 scroll + selection（`type: 'text' | 'node'`，
  保留 `NodeSelection` 如 horizontal rule）；one-shot restore（selection→scroll→focus，
  paint 前 layout effect）。`resolveRestoreSelection` 用 **inlineContent 前置檢查**（本地
  實證 `TextSelection.create` 對非法位置不 throw、只 `console.warn`）。
- **EditorPane gating render**：僅在 `paneState.bufferKey === key`（paneState 已對齊 buffer）
  才 render `TiptapEditor` —— stale paneState 根本不 mount，避免 `React.lazy` cache 後同步
  mount 用 stale viewState 鎖 `didRestoreRef`。

spec 2 輪 + plan 2 輪 codex review；PR R1 標準 + R2 三平行（攻擊/防守/體質）+ R3 + R4 收斂
（D1 unmount race→`useLayoutEffect` / D2 NodeSelection / R3 props-gating→gating render /
R4 cosmetic 閃爍）。161 tests 綠、lint 0、build ok。延後追蹤：#863（markdown↔markdown buffer
切換 transient `Loading editor…` paint，根因既有 `attachPane` post-commit 時序）。

## [1.0.0-alpha.296] - 2026-06-26

### Feat(editor): persist In-App Storage to IndexedDB (fixes #856) (#858)

新建文件預設存到 In-App Storage（`inapp` source），但 `InAppBackend` 底層是純記憶體
`Map` —— 存檔對記憶體成功、UI 顯示已存，但 app upgrade/重啟即遺失，重開 tab（已
persist）變空白 + dirty。改用 **IndexedDB**（復用既有 `openIDB`）持久化，重啟後讀得回。

七個 `FsBackend` method 行為契約完全不變（只換儲存層）：`read`/`stat`/`rename` 對缺失
路徑仍 throw（`EditorPane` mount catch 依賴）；`write` 為單一 readwrite transaction，
auto-create parent dirs 且不驗證 parent 是否為 directory；`rename`/`mkdir` 保留
blind-overwrite；`rename` 只搬單一 entry（non-recursive）。

20 tests（AC1-14，含 persist-across-`closeAllIDB`、空 `Uint8Array` 與非文字 bytes 的
byte 級 round-trip、registry 薄整合驗證）。spec/plan 各一輪 codex review（各 4 finding
全修——點破 AC2 假驗 persist 須先 `closeAllIDB`、`indexedDB.deleteDatabase` 非 Promise
須 wrap）；PR R1 標準 0 finding + R2 三平行（攻擊確認 transaction auto-commit 安全、防守
0 blocker、體質 H1 拆測試 + H3 對齊 IDB upgrade style）收斂。

Scope = 止血（本地持久），不做跨機同步 / 衝突 diff。Known limitation：IndexedDB
per-origin，dev server 與 bundled `.app` 各自獨立 store。延後追蹤：#859（write quota
UI recovery）、#860（`list(missing)` cross-backend contract）。

## [1.0.0-alpha.295] - 2026-06-19

### Feat(editor): close dirty-guard, image viewer zoom, monaco scroll buffer (#852)

Three editor open/close/preview UX improvements addressing user requests:

- **Tab close dirty-guard** — `closeTab` scans the tab's pane tree and prompts
  `window.confirm` when any editor pane's buffer is dirty (including untitled
  drafts with unsaved content); cancelling aborts the close. A shared
  `bufferKey` helper was extracted so the guard, `EditorPane`, and
  `EditorBuffersPane` key buffers identically.
- **Image preview fit/actual zoom** — oversized images (natural > container)
  become click-to-toggle between fit (`object-contain`) and actual size with
  zoom-in/zoom-out cursors and a scrollable container; cached/HMR-complete
  images measure synchronously. Preview state (zoom/natural/objectUrl/error)
  resets on the composite `(identity, backend)` session.
- **Monaco scroll buffer** — `scrollBeyondLastLine: true` keeps a scroll buffer
  past the last line.

Two rounds of codex review plus three incremental rounds and a two-perspective
consulting pass converged the image-preview state machine (R5 0 findings).
86 tests / lint / build green. Follow-ups: #853 (broader dirty protection
across pane-level close / workspace move / window close), #854 (untitled name
race).

## [1.0.0-alpha.294] - 2026-05-05

### Fix(store): SQLite foreign_keys via DSN _pragma + pin trace migration to single conn (#849)

Investigating an 18.3 GB `agent_events.db` revealed that `agent_trace_steps`
had grown to 137,550 rows while `agent_trace_chains` was correctly capped at
exactly 10,000 — a 13.75 : 1 ratio that contradicts the schema-defined
`ON DELETE CASCADE`. Root cause: SQLite's `PRAGMA foreign_keys` is
per-connection, but the daemon enabled it via a single post-`Open`
`db.Exec("PRAGMA foreign_keys = ON")`. With `db.SetMaxOpenConns(4)`, only
~25 % of `BeginTx` calls landed on the FK-enabled connection. Cascade
silently no-op'd on the other 75 %, leaving orphan trace_steps every time
`pruneTraceChains` evicted a chain.

Fix moves FK enablement into the DSN `_pragma=foreign_keys(1)` parameter
(applied by `modernc.org/sqlite` at every new-connection dial) and
removes the post-`Open` `db.Exec`. The `:memory:` test path keeps the
explicit Exec since it always runs at `MaxOpenConns(1)` (no pool-miss
risk).

Round-2 codex adversarial review (3-way fan-out) caught a related
secondary bug: `migrateTraceDB` itself toggled `PRAGMA foreign_keys`
through the pool, conflicting with the new always-on DSN behaviour and
risking failed schema rebuilds against legacy DBs. Migration now acquires
a single `*sql.Conn` at entry, runs all DDL through that pinned
connection, and ends with a one-time orphan cleanup
(`DELETE FROM agent_trace_steps WHERE NOT EXISTS chain`) so existing
deployments are repaired idempotently.

Three rounds of codex review (R1 standard 0 / R2 三平行 4 dedup → 3 fixed
in PR + 1 follow-up issue / R3 standard 0). Follow-up #850 tracks
extracting the magic-`4` pool-size constant for test parameterization.

Live impact verified on mlab: DB shrank from 17 GB to 52 MB after a
manual SQL trim + VACUUM (this PR ensures alpha.294+ daemons no longer
leak orphans in the first place).

## [1.0.0-alpha.293] - 2026-05-04

### Fix(settings): Align modules switchboard with sidebar order (#843)

The Modules Switchboard now mirrors the Settings sidebar's relative
order for the disableable subset. Previously the Switchboard rendered
modules in registration order while the sidebar sorted by
`SETTINGS_ORDER` — same conceptual list, two different visible
sequences. The Switchboard sort key is now the minimum `order` across
a module's purdex contributions; tie-break by `module.id` agrees with
the sidebar's `(order, moduleId, localId)` triple comparator. `Sync`
remains sidebar-only because it is not `disableable`.

Browser and Files now declare a purdex-scope placeholder settings page
via the shared `PlaceholderSettingsSection` component. Every
`disableable` module now has a Settings sidebar entry — invariant **I1**
in the new spec — so the Modules Switchboard ↔ sidebar mental model
stays consistent. The Files purdex placeholder explicitly supersedes
§N3 of `2026-05-03-files-disableable-sr2-spec.md` (PR #833): the prior
"no global settings" UX argument is replaced with an explicit
placeholder so users following the Switchboard never end up with no
sidebar landing.

`SETTINGS_ORDER` is reordered alphabetically by **English short**
sidebar label (B / C / E / F / M / Sync). Constant names stay bound to
module identity (`MODULE_QUICK_COMMANDS` even though the sidebar shows
"Commands") so future label changes do not ripple into the constants
table. Two sidebar labels are shortened: `Performance Monitor` →
`Monitor` and `Quick Commands` → `Commands`. The Switchboard rows,
pane labels and inner page headings continue to use the full
`module.name` ("Performance Monitor" / "Quick Commands") — sidebar =
navigation short, switchboard / pane = identity full. Five new i18n
keys land in both English and 繁體中文.

`listContributions(scope)` is now the single source of truth for
sidebar order with a deterministic `(order, moduleId, localId)` triple
comparator. `SettingsSidebar` and `GlobalSettingsPage`'s default-mount
logic in `SettingsPage.tsx` both consume it without local re-sorts —
for equal-order pairs the auto-mounted default page can no longer
diverge from the first visible sidebar row. `dispatchSettingsContributions`
now throws at registration time when a `disableable` module declares
no purdex contribution, so authoring errors fail loudly instead of
silently leaving the module visible only in the Switchboard. The
Switchboard sort uses `reduce` (not `Math.min(...orders)`) to remove
the spread-arg upper-bound risk if a module ever declares many purdex
contributions.

PR review history: 1 spec-review round (3 medium → all addressed) +
1 plan-review round (4 medium + 2 low → all addressed) + R1 standard
PR review (0 finding) + R2 three-perspective adversarial review
(2 medium + 1 low → all fixed in-PR) + R3 standard verify (1 P2 → fixed
via centralized comparator) + R4 standard verify (0 finding).

Files: `register-modules/index.tsx`, `settings-order.ts`,
`dispatch-settings-contributions.ts`, `settings-contribution-registry.ts`,
`SettingsSidebar.tsx`, `ModulesSwitchboardSection.tsx`,
`PlaceholderSettingsSection.tsx` (new), `register-modules.test.ts`,
`register-modules.quick-commands.test.tsx`,
`settings-order-pr2.test.ts`, `dispatch-settings-contributions.test.ts`,
`PlaceholderSettingsSection.test.tsx` (new),
`ModulesSwitchboardSection.test.tsx`, `en.json`, `zh-TW.json`.

## [1.0.0-alpha.292] - 2026-05-04

### Feat(spa): Trailing-edge sliding debounce for error notifications (#842)

PR-B of the rate-limit-cleanup work (PR-A `#832` shipped at alpha.290).
Adds a 60s trailing-edge sliding debounce in
`useNotificationDispatcher.shouldNotify` for `derived === 'error'`,
keyed by `JSON.stringify([compositeKey, eventName, errorString])`.
First error in a bucket fires the OS notification; subsequent same-key
arrivals within the window extend `silentUntil` and return `false`. A
storm of 100 events ≈ 1 notification (≥99% suppression — spec AC5),
matching the dthn-class scenario where cc was firing ~1.5 Hz of
`PdxStopFailure` during a rate-limit window.

Cleanup runs on `clearSession` / `removeHost` via a single module-level
store subscription (no reverse import) plus a throttled TTL self-cleanup
(at most once per `ERROR_NOTIFY_WINDOW_MS`). The Map is hard-capped at
1000 entries with FIFO eviction so a high-cardinality `errorString`
storm cannot blow up the renderer. The `unread` badge and
`derived === 'waiting'` / `'idle'` paths are deliberately untouched —
debounce only gates desktop notifications, not the in-app sticky state.

Three rounds of codex review (R1 standard 0 / R2 三平行 6 dedup → 4
fixed in PR + 1 spec amend + 3 follow-up issues / R3 standard 0). R2
landed: colon-safe `removeHost` cleanup (\`startsWith\` instead of
\`split(':')[0]\`), throttled sweep + hard-cap, subscribe early-exit on
\`lastEvents === prevState.lastEvents\`, and a rewritten unread-badge
test that actually drives the suppression path. Spec AC9 was amended
retrospectively to mark the LOC cap as informational rather than a
hard ceiling.

Follow-up tracking: #844 (errorString normalization), #845 (extract
`errorNotificationDebounce.ts` module), #846 (test seam naming
review).

## [1.0.0-alpha.291] - 2026-05-03

### Fix(files): Files module disableable + close SR-2 (#833)

Closes SR-2 from codex review of PR #617. The Files module previously
declared its `projectPath` setting via the deprecated
`ModuleDefinition.workspaceConfig` API, whose render path
(`WorkspaceSettingsPage` → `ModuleConfigSection` →
`getModulesWithWorkspaceConfig()`) bypassed the Modules Switchboard's
`useModuleEnabledStore` filter. As a result, marking Files as
`disableable: true` would have been a lie — toggling it off would still
leave the workspace `projectPath` editor visible. SR-2's protective
workaround was to leave Files un-toggleable until the migration landed.

This release migrates Files to the contribution-registry path
(`settings: [{ scope: 'workspace' }]`), wires it through
`dispatchSettingsContributions`'s disable filter, and marks Files
`disableable: true`. The behavior matches Editor / Quick Commands /
Performance Monitor: toggling Files off in `/settings/module-config`
shows the ReloadBanner; after reload, the workspace settings page no
longer renders the Files header / `projectPath` input. Storage shape
is unchanged — `useWorkspaceStore.workspaces[wsId].moduleConfig.files.projectPath`
still backs the value, so all existing readers (`FileTreeView`,
`file-open-bootstrap.ts`) keep working without modification.

The PR also hardens `FileTreeView`'s `projectPath` selector (replacing
an unsafe `as string | undefined` cast with a `typeof === 'string'`
guard, defending against a poisoned-persist payload that could have
caused a render crash) and removes the now-empty
`DEPRECATED_LEGACY_CONFIG_EXEMPT` set in
`dispatch-settings-contributions.ts`. The
`SETTINGS_ORDER.WORKSPACE_FILES = 10` constant is added (first
workspace-scope entry), and the `settings-order.ts` docblock is
expanded to clarify per-scope ordering. Three new i18n keys land in
both English and 繁體中文.

PR review history: 2 spec-review rounds + 2 plan-review rounds +
1 standard PR review (0 finding, approved) + 1 three-parallel
adversarial PR review (0 P0/P1; 4 P2/P3 fixed in-PR; 5 P3 deferred to
follow-up issues #834-#839). The adversarial review caught one critical
escape-hatch issue early — removing the `<ModuleConfigSection>` mount
in `WorkspaceSettingsPage` would have detached the only render path
for any module still using the deprecated `workspaceConfig` API while
the API itself was kept alive — and the mount was restored in commit
`09bcc61e`. Full removal of the legacy renderer + `workspaceConfig`
field + helper functions is tracked as follow-up F-1.

Files: `register-modules/index.tsx`, `dispatch-settings-contributions.ts`,
`FileTreeView.tsx`, `FilesWorkspaceSettingsSection.tsx` (new),
`settings-order.ts`, `register-modules.test.ts`,
`WorkspaceSettingsPage.registry.test.tsx`,
`FilesWorkspaceSettingsSection.test.tsx` (new), `en.json`, `zh-TW.json`,
`docs/specs/2026-05-03-files-disableable-sr2-{spec,plan}.md` (new).

## [1.0.0-alpha.290] - 2026-05-03

### Fix(daemon): PdxStopFailure native subagent detach (rate-limit cleanup) (#832)

Closes the dthn-class accumulation bug where cc subagent failures via
the rate-limit error path leak native `SubagentRef` entries into
`agent_frames.subagents_json`. Empirical observation against dthn (pane
`%50`) showed **3944** native refs accumulated, with **97.9%** of
`SubagentStart` events terminating via `PdxStopFailure` (carrying the
failing subagent's `agent_id`) instead of `PdxSubagentStop`. The
existing `LifecycleStopFailure` handler dropped the payload's `agent_id`
on the floor when `frame != nil`, so native refs had no GC path —
projection broadcasts grew without bound and SPA showed permanent
"subagent in flight" lights.

This release teaches the daemon to detach matching native refs when
`PdxStopFailure` carries an `agent_id`. Three providers (cc/codex/
opencode) now surface `agent_id` in `DeriveResult.Detail`. A new pure
helper `findNativeRefByID` gates the detach so a non-matching payload
never triggers a phantom mutation. The `LifecycleStopFailure` case is
split from `LifecycleStop`; the new branch invokes the new
`mutateSubagentsAndStatusWithRetry` helper which atomically removes the
ref **and** writes `Status=error` in the same `UpsertIfUnchanged`
transaction. Four disjoint outcomes (`Detached` / `RefAlreadyAbsent` /
`FrameMissing` / retry-exhaustion-error) each map to a distinct trace
reason — retry races no longer emit phantom detach traces, and retry
exhaustion no longer collapses into `frame_missing`. New trace reason:
`native_subagent_detached_on_stop_failure`. Existing `frame_missing`
reason reused for concurrent SessionEnd race.

Three rounds of codex review converged across spec (P1×3 → P1×2 → fact
+ P2), plan (P1×3 → P1 + P2×3 → fact), and code (R1 P1 atomic write →
R2 three parallel adversarial 2 high + 2 medium → R3 0 finding). PR
size: ~225 production + ~720 test LOC across 7 commits, exceeding spec
AC9 cap of 500 in service of race-safety guarantees forced by R2 review.

Operational follow-up: dthn pane `%50` retains 3944 historical refs
whose terminal `StopFailure` events predate trace retention; these
won't self-heal via the new code path (cc emits each `agent_id`'s
terminal event exactly once). One-shot SQL cleanup per spec §5
(backup → `BEGIN; UPDATE agent_frames SET subagents_json='[]' WHERE
json_array_length(subagents_json) > 100; COMMIT;` → daemon restart) is
the expected primary recovery path.

PR-B (SPA notification debounce for derived=error during rate-limit
storms) is a separate forthcoming PR per spec §11 sequencing.

## [1.0.0-alpha.289] - 2026-05-03

### Feat(lights): cc Bash sniff delegating flag for codex visibility (#829)

Closes issue #821. When cc invokes `/codex:rescue`, `/codex:review`, or
`/codex:adversarial-review`, the cc tab now shows the **orange dot**
(delegating) instead of the misleading **blue native dot**. Pure cc-side
detection via `PreToolUse(Bash)` command sniff with token-boundary check
on `codex-companion.mjs` — no codex plugin cooperation, no `IsProxy`
invariant pollution, zero dependence on governance phases.

Adds two omitempty fields to `SubagentRef` (`Delegating bool` +
`DelegatingToolUseIDs []string`, invariant
`Delegating == len(DelegatingToolUseIDs) > 0`), a pure-function
`ExtractDelegationHint` extractor with no-regex token detection, two
`mark` / `unmark` helpers mirroring the `upsertProxyRefForBroker`
optimistic-concurrency retry loop, and a handler wiring block that
mark/unmark by `tool_use_id` on cc PreToolUse / PostToolUse /
PostToolUseFailure. SPA `SubagentDots` composes
`is_proxy || delegating` to render orange and exposes a new
`data-delegating` attribute alongside the unchanged `data-is-proxy`.

To reach the new wiring on cc's side, this release upgrades
`PdxPreToolUse` and `PdxPostToolUseFailure` from
`HookHandlingUnsupported` / `HookHandlingIgnored` to detail-only
installable: `cc/status.go` returns `Valid=true` with `Status=""` and a
`tool_use_id` / `agent_id` detail, and `cc/events.go` drops the handling
annotation so `mergeClaudeHooks` writes both keys into
`~/.claude/settings.json` on hook merge.

A handler short-circuit guards detail-only PreToolUse / PostToolUseFailure
from `applyFrameEvent`'s `LifecycleNone+Status=""` fallback so the new
events cannot resurrect torn-down frames as `StatusIdle`. Mark/unmark
lookup excludes `IsProxy=true` refs to keep the visual signal native-only
even if a proxy ref happens to share an ID with a native subagent.
`DelegatingToolUseIDs` is capped at 32 entries with FIFO evict-oldest so
a degenerate hook stream cannot inflate `subagents_json` unboundedly;
the Delegating flag stays true throughout cap enforcement so the
visual signal does not flap.

User action required for existing managed installs: cc's
`~/.claude/settings.json` must be re-merged so `PreToolUse` and
`PostToolUseFailure` hook entries land — run `pdx install hooks`,
relaunch the daemon with the managed-install path, or use Settings →
Hooks → Reinstall. Fresh installs pick up the new keys automatically on
first hook merge.

## [1.0.0-alpha.288] - 2026-05-03

### Refactor(settings): PR-2 — Editor consolidation, Sync modularize, Quick Commands header (#825)

Completes the settings architecture cleanup started in alpha.287 PR-1. The
Editor module's three sidebar entries (`editor` / `link-detect` /
`open-behavior`) collapse into a single Editor settings page with the two
removed sections rendered as embedded subsections; `/settings/link-detect`
and `/settings/open-behavior` URLs alias to `/settings/editor` via the
extended `URL_ALIASES` map. Sync upgrades from a built-in legacy section
to a structural module (non-`disableable`), so it picks up the puzzle icon
and the modules-group ordering naturally without changing engine
lifecycle. Quick Commands settings adopts the Appearance/Terminal header
pattern (bare `<div>` outer, `<h2>` + `<p>` description). All module-owned
order values now flow through `SETTINGS_ORDER` constants — the PR-1
transitional `*_PR1` keys are removed in favor of the final
`MODULE_EDITOR=11`, `MODULE_QUICK_COMMANDS=12`,
`MODULE_PERFORMANCE_MONITOR=13`, `MODULE_SYNC=14` values.

URL alias resolution uses `Object.hasOwn` to guard against
prototype-property collisions (e.g. a hypothetical section with localId
`constructor`) and self-heals to `firstSelectable` instead of a stale
`lastSection` whenever the canonical alias target is unselectable —
covering both fresh-mount and mounted-then-navigate paths.

Follow-up issue #822 tracks extracting `<ModuleOwnedPuzzleIcon>` as a
shared component across the three sidebar callers.

### Fix(agent): ignore detached frames in pane projection (#826)

Pane/session projections now ignore alive-but-detached agent frame rows whose
process no longer belongs to the current tmux pane tree. This prevents stale
standalone Codex broker frames from becoming the tab's top agent after the real
foreground agent exits; when no pane-owned frame remains, the projection emits
`status=clear` so the SPA returns to the terminal icon.

The filter is projection-only and fail-open on transient tmux/process lookup
errors. Detached broker rows remain in the database for telemetry/history; they
are just ineligible to represent the current pane unless their process ancestry
includes the pane PID and their PID start time still matches.

## [1.0.0-alpha.287] - 2026-05-03

### Refactor(settings): PR-1 sidebar order and spacing cleanup (#816)

Adds the first settings architecture cleanup slice: centralises sidebar order in
`SETTINGS_ORDER`, applies the PR-1 transitional settings order, switches puzzle
icons to upright bold styling, and removes duplicate Modules Switchboard padding.
This keeps the visual/sidebar cleanup separate from later Editor consolidation
and Sync modularisation work.

### Fix(spa): silence empty opencode stop notifications (#823)

OpenCode `PdxStop` events derived from `session.status.idle` now carry
`notification_silent=true` metadata. The SPA still applies the idle status for
lights, but skips unread markers and desktop notifications for these empty Stop
events so users no longer see generic `Task completed` notifications with no
actionable content.

## [1.0.0-alpha.286] - 2026-05-03

### Feat(codexbroker): P2 — decision predicates + kill sequence + manual sweep API (#813)

Phase P2 of the codex broker governance series. Builds on alpha.280 P1
inventory; adds decision evaluation (predicates A/B/C + stale-running
detection + emergency overrides E1/E2 + foreign-broker quarantine) and the
operator-driven kill sequence (Step 0 identity verify → audit preimage →
graceful RPC → SIGTERM → SIGKILL → verify-gone → cleanup with socket-inode
verification + audit postscript). New endpoint `POST /api/codex/brokers/sweep`
with `mode=dry-run|apply` and optional `brokerKey` filter.

Mass-kill safety guarantees on mlab steady state (50+ pre-existing brokers
with empty launch registry):

- Unfiltered `mode=apply` issues zero kills against any foreign broker —
  every record's `Reason="foreign_quarantine"` blocks the unfiltered path.
- Operator override `mode=apply&brokerKey=<X>` (the spec §5.1 line 371
  manual override semantic — no separate `force` flag) is the only path
  that can kill a foreign broker, and only when baseline `Kill=true`
  (¬A∧¬B∧¬C ∧ idle expired). Alive predicates ALWAYS protect.
- Identity-mismatch on Step 0 re-verify (and re-verify before every
  signal) auto-quarantines the broker rather than retrying.
- Quarantine load corruption fails closed: apply returns 503 until the
  operator clears the renamed `.bak` file and restarts the daemon.

Concurrency model: two-layer `sync.RWMutex` (`globalApplyMu` + per-broker
`sync.Map[brokerKey]*sync.RWMutex`) with fixed lock order — deadlock
impossible by construction. Unfiltered dry-run takes the global write
lock to avoid lock-storm on 50+ broker scans. `quarantineMu` serialises
shared quarantine state across concurrent filtered applies. Identity
re-verification happens immediately before EACH `syscall.Kill` to defend
against PID-reuse during the audit/graceful window.

Plan + PR went through 5 rounds of plan review (converged at 0 finding)
and 4 rounds of PR review (R1 standard + R2 three-parallel adversarial
+ R3 standard + R4 standard final 0 finding) before merge. 27 commits
on top of plan v5; full audit trail in commit messages.

Known follow-up gaps tracked as separate issues:

- Darwin sockverify: CGo-free libproc binding (currently uses `lsof`
  fallback; spec §5.4 line 472 explicitly accepts `lsof` as bounded
  fallback).
- Linux sockverify: inode-based verification via parsing `socket:[inode]`
  fd targets (currently forces `lsof` fallback after R1 finding caught
  path-comparison bug).
- Integration test: full sweep contract assertion gated on real broker
  discovery; sandbox uses `PDX_INTEGRATION_SKIP_ON_DISCOVERY=1` env var
  opt-out.

P3 (automatic triggers, daemon tick, ExitWorktree hook, SPA dashboard)
deferred to later PRs per spec §9 build sequence.

## [1.0.0-alpha.285] - 2026-05-03

### Fix(spa): clear stale subagent dots (#814)

Fixes stale subagent dots when the backend explicitly broadcasts
`subagents:null`. The SPA now treats explicit `null` the same as an empty
subagent list while preserving the existing no-op behavior when the field is
absent. The agent projection path also serializes empty subagent lists as `[]`
instead of `null` so reconnect and hook broadcasts stay consistent.

## [1.0.0-alpha.284] - 2026-05-02

### Feat(codex): W6-6 ScreenChange ProbeIntent for permission-reply detection (#799)

Last piece of the Lights rebuild W6 series. Codex emits no PdxStop hook when
the user replies to a `PdxPermissionRequest` waiting state by pressing enter
to approve — the lights would stay stuck in `waiting`. W6-6 introduces a
ScreenChange ProbeIntent that uses `Prober.Watch` to observe the 10 lines of
pane content above the input row; once ScreenStable lands, the first
ScreenChanged event emits a Signal that flips status to `running`.

PR-level highlights:

- 2-phase + 2-case truth table contract (v5 user reframe).
- Reuses W6-3 ProbeIntentProvider interface + dispatcher 5-case lifecycle.
- Reject path race covered by J3 dispatcher generic pre-grace (alpha.281).
- v6.1 detector close-race fix: mutex-protected `closed bool` flag.
- v6.2 watchLoop baseline retry: capture failure no longer cleanup-returns;
  500ms loop waits for the next successful capture.
- Ownership-aware teardown: Prober.WatchHandle + StopWatchOwned avoid
  same-paneID re-arm cancelling new watcher (W3 legacy / sweep keep
  StopWatch since they own target end-to-end).
- Post-grace by-Kind: ScreenChange (hook-armed observer) skips the 2s
  suppression that ProcessDead (race competitor) needs.
- Spec §0.4 / §4.3 / §8 reframe (F7-3): baseline failure is a probe-layer
  transient, not a detector lifecycle signal; 3 drift anchors guard against
  future regression.
- 6 rounds of PR codex review converged: R1 P2 (StopWatch race), R2 three-way
  adversarial (3 findings), R3 P2 (post-grace by-Kind), R4 P2 (Watch baseline
  fail), R5 P2 (tight rearm loop), R6 standard 0 finding.
- Known limitation: case 2 (quick-approval / fast-with-output) — armed=false
  drops ScreenChanged → no emit → idle via PdxStop, lights waiting → idle
  skips running phase (by-contract, spec §0.5).
- F4 (50ms cleanup sleep brittle) → issue #800.

Closes #762 (Lights rebuild umbrella).

## [1.0.0-alpha.283] - 2026-05-02

### [L2] Codex broker turn-aware proxy detach on Stop (#801)

Codex broker (long-lived `codex resume <thread>`) under cc/opencode parent
now uses turn-aware proxy ref identity for Stop detach. The pre-L2
SourceTurnID-blind wildcard detach sometimes dropped the wrong turn's ref
on legitimate intra-broker turn-change races; L2 stamps each
UserPromptSubmit/PreToolUse upsert with a `turn_id` (parsed from codex
raw_event) and routes Stop through three sub-cases (spec §3.3.D):
targeted detach when Stop's turn matches; skip when broker's ref carries
a non-empty turn but Stop's turn is missing/malformed (preserves the
dot — Stop is unreliable signal there); empty-turn fallback with
turn-aware filter (`turnID==""`) re-verifying the empty state inside the
optimistic-concurrency loop, so a concurrent upgrade is no longer
wildcard-dropped.

PR-level highlights:

- Spec v5 final after 5 rounds of codex spec review.
- 11 TDD commits across 3 phases (P1 catalog/parse, P2 helpers, P3
  dispatch + 22 spec §5 rows + concurrency rows 14/15/15b/16/19).
- 4 rounds of PR codex review:
  R1 standard → 1 blocker (PreToolUse no-parent short-circuit).
  R2 three-way adversarial → 10 findings adopted (A1 TOCTOU / A2
  turn_id preserve / A3 sender_start_time canonicalization / A4
  turn_id length cap / D1-D4 defensive rows + trace audit / F3 file
  split / F6 line-number cleanup).
  R3 standard → 1 P2 (round-2 A2 vs round-3 P2 trade-off boundary).
  R3 consulting (independent codex session) → confirmed physical
  trade-off, recommended option-1 revert.
  R4 standard → 0 finding.
- A2 production reverted to unconditional overwrite (round-3 P2
  resolution); spec §3.5 known-limitation #5 documents the trade-off.
- 5 follow-up issues opened: #803 (race-mode controlled interleaving),
  #804 (`applyFrameEvent` dispatcher split), #805
  (`frame_ops_proxy.go` extraction), #806 (`TurnIdentityProvider` for
  cc/opencode L3 forward-compat), #808 (explicit unknown/new-generation
  turn state to break the SourceTurnID single-field trade-off).
- A6 (PID-reuse triple collision) and F5 (SenderTurn naming) accepted
  without change.

## [1.0.0-alpha.282] - 2026-05-02

### Fix(spa): preserve dot-slash terminal file links (#807)

Terminal file-link detection no longer slices `./CLAUDE.md` into `/CLAUDE.md`.
The absolute path matcher now treats `.` as a path-prefix boundary, while the
relative-slash matcher continues to recognize the full `./...` path.

## [1.0.0-alpha.281] - 2026-05-02

### Feat(agent): J3 ProbeIntent dispatcher bidirectional graceWindow (#797)

W6-6 ScreenChange ProbeIntent 前置 PR — 將既有 `probeGraceWindow`
（post-direction only：hook 後 2s drop probe）擴成雙向。dispatcher
`consumeSignals` 在進入 `applyProbeGuards` 之前先做 signalAt-based
post-grace pre-check（preserve 2s threshold），再 hold 300ms
`probeIntentPreGraceWindow`，期間 hook 到 → drop pre-grace；ctx cancel
→ classify 為 hook race vs lifecycle cancel。

Generic 對所有 ProbeIntent Kind 適用（W6-3 ProcessDead / W6-6
ScreenChange / 未來 Kind），per fix-spec §3 不為單一 Kind 特化約束。

mlab live verify：
- A11 SIGKILL ✅ PASS — codex pid SIGKILL → status=error end-to-end ≤1.5s
- A12 daemon replay ✅ PASS — restart → replayStatus → re-arm ~2s
- A9/A10 deferred to W6-6 PR（W6-6 ScreenChange 未 merge → 無可量對象）

5 輪 codex review 收斂（R1 standard 0 / R2 三平行 adversarial 3
finding 全採納 / R3 P1 + R4 P2 + R5 0 finding），spec v7.5 + plan v3。
W6-3 spec §9.14 加 generic-Kind drift anchor。

R13 boundary race 為 known limitation — fail-fast handling：mlab
A9 PASS = R13 acceptable；A9 fail = followup issue 加 per-event trace。

## [1.0.0-alpha.280] - 2026-05-01

### Feat(codexbroker): P1 inventory endpoint (read-only, governance phase 1) (#792)

First step of the codex broker / app-server governance feature (kickoff
`kickoff_codex_broker_and_lights_governance.md` → governance P1). Adds a
new daemon-side `internal/codexbroker` package and `GET /api/codex/brokers`
endpoint that enumerates every codex broker process, state directory, and
socket directory visible on the host with full attribution and a closed-
list anomaly schema. **Read-only** — zero side effects on broker
processes or filesystem state.

Live mlab verification at ship time:

```
summary.total=66 withProcess=42 withStateDir=55 withSocket=42
scanSourceTimeouts=[] partial=false p95=69ms
ps count 42→42 unchanged across two scans
```

P2 (decision + kill), P3 (trigger), P4 (SPA dashboard) and the four lights
phases (L1–L4) ship in subsequent PRs per the same kickoff. Issue #668
(codex broker orphan tracking) stays open until P3 lands.

Two rounds of codex review (R1 standard, R2 three-parallel adversarial)
caught nine blockers total — all fixed in-PR. Three deferred follow-ups
tracked as #793 (hung-syscall goroutine leak under truly-stuck FS),
#794 (AnomalyCode runtime emission enforcement), #795 (file-quality
cleanup: split `reconcile.go` / unify naming / unexport / move test
seams).

Spec drift correction along the way: original §3.4 specified NFC + APFS-
aware case-fold inside `BrokerKey`, but live integration revealed this
desyncs from codex CLI's own state-dir naming (raw `realpathSync.native`
bytes). The hash is now byte-faithful to codex; collision detection
moves to a P2 anomaly check rather than touching the primary key.

## [1.0.0-alpha.279] - 2026-05-01

### Feat(agent): cc PdxPostToolUse → running status (W6-1a) (#790)

Promotes cc `PdxPostToolUse` from `Handling=Ignored` (non-installable) to a status-emitting hook with `EmitsStatus=[Running]`, closing the W5-1 gap where lights stayed yellow after a permission grant until `PdxStop` flipped to idle — skipping the running phase entirely. Now `PostToolUse` gives `waiting → running` a real hook signal.

mlab live verify (cc 2.1.123): `Write(/tmp/...)` permission flow shows `PdxPermissionRequest` → waiting, `PdxNotification(permission_prompt)` → waiting (dual fire confirmed, both map to waiting), user approves, **`PdxPostToolUse` → running** (the W6-1a fix), then `PdxStop` → idle. cc installable hook count goes from 9 to 10; existing installs auto-pick-up the new hook on next `pdx setup --agent cc`.

Also includes a documentation-only outcome for W6-2 (cc compact): `SessionStart(source=compact)` does fire on `/compact` end but the daemon's `compact_ignored` branch is intentional and trace-only. Pre-compact `PdxStop` already settles status to idle in all observed scenarios, so no code change is needed for W6-2 (verified-only).

## [1.0.0-alpha.278] - 2026-05-01

### Fix(daemon): drop sweep idle_timeout — alive idle session no longer cleared (#788)

Removes `sweep.go` path 3 (`idle_timeout`) which marked frames as cleared when their PID was still alive but no hook had bumped `LastSeenAt` for ≥ 1 hour. The invariant was wrong: cc / codex / opencode are long-running processes and an idle session for an hour is normal usage — not a "logically dead" agent. True process death is already covered by `pid_dead` (PID gone) and `pid_reused` (`process_start_time` mismatch) without time bound. The 1h destructive sweep led to all SPA tab lights being silently cleared after a quiet hour even though every agent was still running; `clearSession` then dropped the keyed status map entry and indicator dots stopped rendering until the next hook landed.

`frameIdleThreshold` const is removed. `nowFn` is preserved (still consumed by `canonicalizePane` / `pruneDeadProxyRefs` / `afterFrameCleared` for deterministic broadcast timestamps in tests). Future SPA-side idle-duration display goes through `frame.LastSeenAt → broadcast → SPA` directly (issue #787) and does not require sweep-side detection.

Tests: IS1 rewritten as inverted invariant (`alive + identity-verified frame with stale LastSeenAt is preserved + emits zero broadcast`); IS2 / IS3 / IS5 / IS6 deleted (all defensive against the removed path); pid_dead orphan watcher cleanup still covered by IS4; `setProjectionTopStatus` narrow-update contract still covered by IS7.

## [1.0.0-alpha.277] - 2026-04-30

### Fix(spa): W6-LightsUI 4 lights polish (#783)

Closes #762. Four self-contained SPA lights fixes after W1 audit + W6-3+W6-4 ship: `useAgentStore` clears leftover unread when status flips to running so a background tab marked unread by a prior waiting/idle no longer keeps the red dot indefinitely; `TabStatusIndicator` overlay dot drops `animate-breathe` when `isUnread` so the unread tint stays static; `TabStatusIndicator` switches `WarningDiamond` weight from `duotone` to `fill` for crisper error rendering; `SubagentDots` now colors by `is_proxy` (proxy = orange, native = blue) instead of agent type family and drops the hollow-ring proxy variant since the parent agent icon already conveys type.

## [1.0.0-alpha.276] - 2026-04-30

### Perf(daemon): hook pipeline fast-path + sqlite tail tuning (#776)

Hook hot path no longer fans out `1 + 7×S` tmux subprocesses per event. The agent module accepts a new `tmux_session_id` field in hook payloads and resolves the session code via `EncodeSessionID` — a pure O(1) function that bypasses the name cache and `ListSessions` entirely when the ID is present. Legacy hook clients (older `pdx hook` binaries that send only `tmux_session`) still work via a `LookupCodeByName` fast-path with 250 ms TTL plus three-layer invalidation (HTTP handlers, watcher hash change, watcher wait-for) and a name-path fallback safety net.

The session module gains a `nameCache` keyed by tmux name and invalidated on create/rename/delete, on watcher-detected hash changes, and at the start of every `broadcastSessions` call so external `tmux kill-session` / `new-session` / `rename-session` operations are reflected within ~50 ms via the daemon's tmux hooks. The agent module logs a startup line indicating whether the fast path is active so decorator-wrapped providers that drop the `LookupCodeByName` interface are visible at boot.

SQLite stores add `_pragma=busy_timeout(500)` and pool caps (4/4 for the agent DB shared by frames and trace, 2/2 for meta) — transient contention now waits up to 500 ms instead of aborting, without changing durability (`synchronous=FULL` retained).

Hook session resolution is labelled with `id_path` / `id_empty` / `malformed_id` in broadcast log reasons so operators can grep migration status. The `TestResolveSessionCodeFromHook_TrustsIDOverMismatchedName` test locks the trust contract: a valid `tmux_session_id` is authoritative; the daemon does not cross-validate against `tmux_session`/`tmux_pane_id`.

Live verify on mlab measured 5/6 hook chains completing `[hook] trigger → [broadcast]` within the same daemon-log second (vs ~5 s baseline). Follow-ups: #781 (system-wide session_id-keyed identity) and #782 (`log.Lmicroseconds` for sub-second timing measurement).

Closes the round-2 stale-name-reuse race finding via the immutable `tmux_session_id` path; legacy fallback retains the bounded race window covered by `TestLookupCodeByName_NameReuseAfterInvalidate`.

## [1.0.0-alpha.275] - 2026-04-30

### Feat(spa): add performance monitor settings page (#779)

Moves Performance Monitor discovery into Settings as a module-owned page while keeping the existing `memory-monitor` module id and pane renderer for restore compatibility. The settings page is hidden when the monitor module is disabled.

Performance Monitor is no longer offered as pane replacement content from `NewPanePage`, matching the settings-first entry point.

## [1.0.0-alpha.274] - 2026-04-30

### Feat(agent): W6-3+W6-4 codex error/clear ad-hoc ProbeIntent — first per-agent probe (#765)

Lights system gains its first ad-hoc per-agent ProbeIntent — codex `ProcessDead` detector polls senderPID + paneID and emits a single `Signal` once either invariant fails, recovering the missing `StopFailure` (W6-3 error light) and `SessionEnd` (W6-4 clear light) transitions that codex CLI 0.124.0 does not fire.

The dispatcher introduces a 5-case lifecycle (case 1 noop / 2 arm / 3 stop / 4 already-armed noop / 5 target-mismatch rearm), a generation-scoped teardown helper, fail-closed handling for unsupported kinds, and replay recovery so daemon restart re-evaluates active intents.

Five rounds of codex review surfaced 11 findings, all addressed: F1 graceWindow strand + rearm, F2 generation-scoped teardown race, F3 PID reuse → issue #777, F4 hint validation revert, F5+F7 reconcile/stopAll observability + helper extraction, F6 unsupported-kind fail-closed, F8 `HasPane (bool, error)` + tmux transient-vs-confirmed-absent semantics, F9 nil-traceSink panic, F10 "no server running" → confirmed absence.

Closes #698 (daemon restart activeWatchers recovery). Status flips: W5-4 (codex error) ✅ / W5-5 (codex clear) ✅.

## [1.0.0-alpha.273] - 2026-04-30

### Chore(spa): update performance monitor labels (#774)

Updates the monitor module display name, pane label, provider title, and module description to Performance Monitor in English and Traditional Chinese while retaining the existing `memory-monitor` pane kind and module id for restore compatibility.

## [1.0.0-alpha.272] - 2026-04-30

### Test(spa): cover disabled monitor polling (#772)

Adds a regression test for the disabled `memory-monitor` module path. The test verifies that the disabled module placeholder renders instead of mounting `MemoryMonitorPage`, and that daemon monitor fetches plus Electron client metric pulls remain inactive across fake timer advancement.

## [1.0.0-alpha.271] - 2026-04-30

### Feat(spa): poll monitor client metrics (#770)

Polls Electron client metrics through `getProcessMetrics()` at the effective Performance Monitor refresh interval instead of relying on fixed push updates. Matching rows now render client CPU, memory, and browser-view state.

Client metrics are scoped by both pane id and pane kind so stale browser metrics are not shown after a pane id is reused for non-browser content.

## [1.0.0-alpha.270] - 2026-04-30

### Feat(spa): show selected monitor top processes (#768)

Adds selectable Performance Monitor pane rows with an accessible control linked to the top-process detail panel. Selecting a daemon-backed session row now shows daemon-reported top processes with PID, CPU, and memory details.

Selected rows without daemon metrics or reported top processes show explicit empty states, and the new labels are localized in English and Traditional Chinese.

## [1.0.0-alpha.269] - 2026-04-30

### Feat(spa): add monitor config controls (#766)

Adds Performance Monitor controls for refresh interval and top-process limit using daemon-provided bounds. Updates persist through the monitor config API and trigger an immediate monitor reload so new refresh cadence takes effect without waiting for the old timer.

The controls preserve dirty drafts across polling refreshes, avoid submitting blank drafts, and guard pending update responses across host endpoint changes and active-host switches.

## [1.0.0-alpha.268] - 2026-04-30

### Feat(spa): fetch monitor rows per host (#763)

Fetches monitor snapshots for every distinct host-bound `tmux-session` pane, so Performance Monitor rows can show daemon metrics for non-active hosts without colliding on equal session codes.

Active-host summary rendering now remains independent from slow or failing secondary host snapshots. Secondary row snapshots settle per host, use bounded timeouts, preserve last matching metrics across active refreshes, and are invalidated when the host record or endpoint identity changes.

## [1.0.0-alpha.267] - 2026-04-30

### Feat(spa): show monitor session metrics (#760)

Wires daemon session metrics into Performance Monitor rows for active-host `tmux-session` panes. Matching rows now display daemon CPU, memory, and process count using the current monitor snapshot's `sessions[].session_code` data.

Rows from other hosts and active-host session panes missing from the snapshot stay explicitly marked as not wired, avoiding accidental cross-host metric attribution. Daemon unavailable reasons now use localized stable reason text.

## [1.0.0-alpha.266] - 2026-04-29

### Feat(spa): show monitor pane rows (#758)

Adds the first Performance Monitor tab-row slice. The page now renders an Open Panes table with one row for every open leaf pane, including split-pane leaves, and shows stable `{tabId, paneId}` identity plus pane kind.

Rows follow the active workspace tab order when present, falling back to global tab order otherwise, and skip stale tab ids safely. Client and daemon metric cells are explicitly marked as not wired yet so later slices can attach Electron client metrics and daemon session matching without changing row identity.

Adds localized Open Panes labels and empty-state text in English and Traditional Chinese.

## [1.0.0-alpha.265] - 2026-04-29

### Feat(spa): show monitor host summary (#756)

Rewrites `MemoryMonitorPage` into the first daemon-backed Performance Monitor UI slice. The page now fetches monitor config and snapshots for the active host, no longer requires Electron metrics to render, and displays host CPU, memory, disk, sample time, and effective refresh cadence.

Snapshot polling follows the daemon config interval, avoids fallback daemon requests when the active host record is missing, preserves the last host summary through transient refresh failures, and keeps retrying after errors. Loading/error states now expose live-region roles.

Adds localized Performance Monitor host-summary labels and unavailable reason text in English and Traditional Chinese. Full tab table, client metrics, and monitor settings controls remain deferred.

## [1.0.0-alpha.264] - 2026-04-29

### Feat(spa): add monitor host API helpers (#754)

Adds SPA TypeScript types for the daemon monitor snapshot and config contracts, including nullable host/session metrics, top processes, and config bounds.

Adds `fetchMonitorSnapshot`, `fetchMonitorConfig`, and `updateMonitorConfig` helpers through `hostFetch`, with tests covering auth headers, non-OK errors, JSON PUT bodies, and nullable response typing.

This is the first SPA integration slice for the rewritten Performance Monitor; UI wiring remains deferred.

## [1.0.0-alpha.263] - 2026-04-29

### Feat(monitor): stabilize API contract fields (#752)

Adds handler-level monitor API contract coverage for snapshot JSON shape, stable units, sample timestamps, config bounds, and partial config updates.

Monitor host metrics and session daemon metrics now serialize `unavailable_reason` as a stable nullable field. Available metrics return `null`, while pending or unavailable metrics return the corresponding reason string, giving future SPA consumers a consistent response shape.

This remains a daemon-only API contract slice; SPA host API types and UI integration remain deferred.

## [1.0.0-alpha.262] - 2026-04-29

### Feat(monitor): single-flight snapshot sampling (#750)

Adds single-flight coordination for stale daemon monitor snapshot sampling. Concurrent stale snapshot requests now share one in-flight collection instead of multiplying host, tmux, or process-table scans.

Expensive snapshot collection now runs outside the monitor snapshot state lock, so monitor config updates can invalidate cache without waiting for collectors. Cache writes are guarded by a snapshot generation so in-flight results from an older config cannot repopulate stale cache after invalidation.

This remains a daemon-only, non-breaking slice; SPA UI and daemon CPU-delta refinements remain deferred.

## [1.0.0-alpha.261] - 2026-04-29

### Feat(monitor): add bounded top processes (#748)

Adds bounded daemon `top_processes` for monitor session metrics. Top processes are sorted deterministically by CPU descending, memory descending, then PID ascending, and are truncated by the effective daemon `top_process_limit` config.

Session totals remain inclusive of all pane root processes and descendants even when the top-process list hides lower-ranked processes. Unavailable session metrics now serialize `top_processes` as an empty array while keeping numeric daemon fields as explicit `null` values.

This remains a daemon-only, non-breaking slice; SPA UI, single-flight sampling, and daemon CPU-delta refinements remain deferred.

## [1.0.0-alpha.260] - 2026-04-29

### lights rebuild W3+W4: revert ProbeProfile framework + observability dev log (#744)

Reverts the always-on probe framework introduced by Phase 4a-1 (issue #719) and adds cross-layer dev-log observability so future W5/W6 status-bug work can see exactly which step the chain stopped at.

**W3 — Framework revert** drops `ProbeProfileProvider` interface, `ProbeProfile` struct, the cc adapter, and the always-on `manageActivityWatch` policy. `startWatch` now takes plain `WatchOptions`; default behavior is no-op (the orchestrator only manages a watch when an explicit ad-hoc `ProbeIntent` arrives, which W6 will introduce per agent).

**W4 — Observability** adds dev-log lines `[hook] trigger / [derive] verify_passed|skipped / [handler] frame_apply|projection_built|invalid_skip / [broadcast]` covering every hook chain branch, plus a TraceStore step coverage audit recorded inline in the `internal/module/agent/trace.go` package comment (8 hook paths × 5 trace steps with reasonable-absence rationale). All `log.Printf` (incl. argument formatting) is gated by `if isDevMode() { ... }` so production performs no I/O. `chain_id` is propagated end-to-end so a single hook chain greps as a connected 5-step sequence.

Until W6 lands the per-agent ad-hoc `ProbeIntent` for cc spinner / codex error and clear / opencode running mid-state, those status branches will not auto-promote — accepted regression observation period per spec §0.2 (the framework was the wrong abstraction and is now removed cleanly). Issue #719 always-on `[probe]` chatter no longer appears in daemon log.

Closes #719.

## [1.0.0-alpha.259] - 2026-04-29

### Feat(monitor): preserve unavailable session metrics (#745)

Keeps daemon monitor session rows visible when session daemon metrics cannot be mapped or collected. tmux pane listing failures, process table failures, missing pane mappings, and missing process data now return stable `unavailable_reason` values instead of failing the snapshot request.

Unavailable daemon metric fields are serialized as explicit `null` values for `cpu_percent`, `memory_bytes`, and `process_count`, while available session metrics continue returning numeric values. Reasons are scoped per session so one session's failure state does not overwrite another session's known mapping state.

This remains a daemon-only, non-breaking slice; SPA UI, top-process selection, and single-flight sampling remain deferred.

## [1.0.0-alpha.258] - 2026-04-29

### Feat(monitor): aggregate session process totals (#742)

Adds the next daemon monitor slice: live Purdex sessions are now represented in monitor snapshots by `session_code` and tmux session identity. The monitor reads the session provider from the core registry and aggregates process totals for each Purdex tmux session.

Session totals include every pane in the tmux session, including inactive panes, and include each pane root process plus descendants. Processes are de-duplicated by PID across panes so split/session layouts do not double-count shared descendants.

This remains a daemon-only, non-breaking slice. SPA UI, unavailable-state rendering, top-process selection, and single-flight sampling are still deferred to later PRs.

## [1.0.0-alpha.257] - 2026-04-29

### Feat(monitor): add daemon performance monitor foundation (#740)

Adds the first non-breaking daemon foundation slice for the performance monitor. The daemon now registers an independent `monitor` module with `/api/monitor/config` and a cached `/api/monitor/snapshot` skeleton, separate from agent trace monitor routes.

The config API persists monitor refresh interval and top-process limit settings with safe bounds and clamping. Snapshot caching now reuses fresh samples, invalidates on config updates, and avoids tmux/process sampling until later session-metrics slices.

This release also adds backend primitives for subsequent tab/session metrics: host CPU/memory/disk summaries, all-pane tmux listing, process table parsing, and pane process-tree aggregation. No SPA monitor UI or user-visible session metrics are enabled yet.

## [1.0.0-alpha.256] - 2026-04-29

### Refactor(agent): W2 cleanup-followup — simplify cc/codex cleanup sets to two-set union (#738)

Plan §5.3 CLEANUP-T1 follow-up to W2 alpha.255 ship. Drops the `ccLegacyEventNames` / `codexLegacyEventNames` static fixtures introduced in P3-T4a as a transition safety net while `HookEventSpec.Name` was being removed.

cc and codex catalogs use a 1:1 upstream/Pdx mapping where each installable spec's `UpstreamKeys[0]` is identical to the pre-W2 command-tail token (e.g. `Stop`). The three-set union (`UpstreamKeys ∪ PurdexName ∪ legacy Name`) collapses to a two-set union once legacy Name is dropped — pre-W2 tokens are still recognised via the UpstreamKey leg.

**Behavioural delta: zero.** Verified by codex round-1 review (0 finding) plus the existing `TestC{c,odex}OwnedCleanupEventNames_CleansLegacyAndNew` round-trip tests, which mix legacy `Stop` with new `PdxStop` entries and assert only the canonical Pdx entry remains after reinstall.

**Spec deviation** — plan §5.3 anticipated a `TestCcOwnedCleanupEventNames_OldFixtureNotCleaned` test asserting that "舊命令會殘留（已不在 cleanup set）" after CLEANUP-T1. That assertion isn't implementable for cc/codex's actual catalog because legacy Name strings == UpstreamKeys[0]; the cleanup set still contains them via the UpstreamKey leg. PR body documents the deviation.

Net diff: −145 / +27 lines across 4 files.

## [1.0.0-alpha.255] - 2026-04-29

### Feat(agent): W2 Phase 3 — opencode catalog naming separation + transition cleanup (#736)

Final phase of the W2 catalog naming separation. opencode joins cc (alpha.251) and codex (alpha.254) on the unified `PurdexName` / `UpstreamKey` / `Lifecycle` schema, and the legacy fallback path is fully removed. All three agents are now metadata-driven end-to-end.

**opencode catalog** — 65 entries gain `PurdexName` (Pdx-prefixed) / `UpstreamKeys[]` / `Lifecycle`. The cross-agent invariant runner flips opencode from reverse `LegacyShape` → forward `AllForwardInvariants`. `DeriveStatus` switch cases route on `PdxXxx`; legacy literals miss the catalog. `PdxPermissionRequest` keeps its multi-source mapping (`UpstreamKeys = ["permission.asked", "question.asked"]`) per spec §2.3.

**Plugin emit via `PURDEX_EVENT` const** — installer injects an 8-key Go-sourced JS const into `~/.config/opencode/plugins/pdx-agent-hooks.js`; all `emit(...)` callsites now reference `PURDEX_EVENT.PdxXxx` instead of legacy string literals. Magic marker `pdx-managed:opencode-hooks:v1` unchanged. Reinstall is idempotent (sha256 stable on second run).

**Transition cleanup** — `legacy_hook.go` deleted in full; `isLegacyHookForUnmigrated` predicate removed; `classifyLifecycle` now dispatches purely on catalog `LifecycleEventKind` metadata. Hot path (`handler.go`) and cold path (`sendSnapshot` / `replayFromDB`) both skip+clean events whose stored `event_name` no longer maps to a catalog `PurdexName` (R2-Attack follow-up `P3-T6.2` covers SPA cold reconnect).

**Schema cleanup** — deprecated `Name` field on `HookEventSpec` removed (`P3-T4`); `event_name` JSON unmarshal alias on `EventRequest` removed (`P3-T5`). The cleanup helper's legacy event-name set is now derived from a fixture constant (P3-T4a), unblocking the field removal.

**Round-2 follow-ups**

- **P3-T6.1** (R1 P2 → alpha-acceptable): `replayFromDB` rejects stored opencode legacy `event_name` rows on daemon restart. Spec §0 + plan G1 + memory feedback `no_alpha_migration` accept the cross-version transition gap; the negative test pins the alpha boundary.
- **P3-T6.2** (R2-Attack medium): SPA cold reconnect path (`sendSnapshot` / `replayFromDB`) was rebroadcasting `Valid=false` rows keyed by `raw_event_name`, causing stale legacy events to resurrect on a freshly-upgraded daemon. Mirrors `handler.go:230` hot-path cleanup in both replay paths.
- **P3-T7.1** (R2-Defense medium): `pluginSimState` contract simulator was a second SOT emitting legacy literals; renamed all 9 callsites to `PdxXxx` to match the post-P3-T3 plugin emit contract.

**File-Health (R2)** — approved.

**Upgrade note** — after upgrading to alpha.255, run:

```
pdx install --reinstall --agent opencode
# (cc/codex already migrated at alpha.251/.254)
```

Pre-existing opencode plugins (loaded into already-running opencode processes) continue emitting legacy literals until opencode is restarted; the daemon classifies these as `event_not_in_catalog` (no broadcast). User-facing impact: opencode lights stop updating until user reinstalls plugin and restarts opencode. This is the alpha breaking change deliberately accepted in spec §0.

**Live verify (mlab)** — fresh `tmux new-session 'opencode'` produced 3 chains (`PdxSessionStart` / `PdxUserPromptSubmit` / `PdxStop`), all 5-step `trigger → verify_passed → frame → projection → broadcasted`. Pre-existing opencode session emitting legacy literals classified `event_not_in_catalog → skipped` — confirms fallback predicate is gone.

## [1.0.0-alpha.254] - 2026-04-29

### Feat(agent): W2 Phase 2 — codex catalog naming separation + lifecycle metadata (#730)

Codex agent catalog migration mirroring cc's W2 Phase 1 (alpha.251). Daemon-internal canonical event id (`PurdexName`, `Pdx`-prefixed) is now decoupled from the upstream Codex hook event name (`UpstreamKey`). Lifecycle dispatch goes through catalog metadata; the legacy fallback predicate retains only the opencode case until Phase 3 ship.

**Catalog** — codex's 11 entries gain `PurdexName` / `UpstreamKeys` / `Lifecycle`. Cross-agent invariant runner flips codex from reverse `LegacyShape` → forward `AllForwardInvariants`. DeriveStatus switch cases rename to `PdxXxx`; legacy literals now miss the catalog (regression-pinned by `TestCodexDeriveStatus_LegacyNames_Invalid` on the 9 negatives).

**Installer boundary** — `~/.codex/hooks.json` keyed by `UpstreamKey` (e.g. `Stop`, `SessionStart`); command tail is `PurdexName` (e.g. `pdx hook --agent codex PdxStop`). `CheckHooks` mirrors the boundary on the read path. `codexKnownEventNames` / `codexOwnedCleanupEventNames` derive from the catalog as a three-way union (`UpstreamKeys ∪ PurdexName ∪ legacy Name`).

**Predicate** — codex case removed from `isLegacyHookForUnmigrated`. Only opencode remains until Phase 3.

**SPA** — codex event-name fixtures updated to `Pdx` prefix.

**Round-2 follow-up (P2-T7)** — `TestHandleEvent_CodexLegacyEventName_IsCatalogMiss` pins the post-P2 boundary: a user upgrading the daemon without running `pdx install --reinstall --agent codex` ends up with hooks emitting legacy literals that now hit the catalog-miss path. Spec §0 + plan G1 deliberately accept this for alpha — reinstall is the user-facing transition trigger. The negative test makes future drift re-introducing a fallback path observable.

**Upgrade note** — after upgrading to alpha.254, run `pdx install --reinstall --agent codex` (and `--agent cc` if not already done at alpha.251). Legacy hooks will silently miss the catalog instead of broadcasting status updates until reinstalled.

## [1.0.0-alpha.253] - 2026-04-29

### Fix(spa): wide-mode workspace drag — reorderable, smooth, axis-locked (#732)

Restore workspace reordering by drag in the wide ActivityBar. The fix bundles four related issues uncovered iteratively while debugging:

**Root cause** — `WorkspaceRow`'s name button (`flex-1`, the dominant click target) and Plus button called `onPointerDown stopPropagation()`, blocking dnd-kit `PointerSensor` from receiving pointerdown so workspace drag never activated. The "drag hijack" guard added in 259c2a4f / Phase 3a was redundant: `activationConstraint: { distance: 5 }` already separates click-without-movement from drag-with-movement.

**Three follow-up fixes** surfaced once drag was reachable again:

- *Sibling rows bouncing*: each row registers two overlapping droppables (sortable wrapper + header `useDroppable`). `pointerWithin` returned both, `over` flickered between ids, `verticalListSortingStrategy` displaced siblings only when over.id was in items → bounce. `customCollisionDetection` now filters `droppableContainers` by active drag type.
- *Drag escapes the list / horizontal drift*: add `restrictWorkspaceDrag` modifier mirroring `ActivityBarNarrow` — Y-axis lock + scroll-zone bbox clamp; short-circuits for tab drag to preserve cross-workspace movement.
- *Text blurs / appears compressed*: switched from `CSS.Transform.toString(transform)` (3-axis translate + scaleX/Y, non-integer y → sub-pixel blur) to `translate3d(0, Math.round(y), 0)`, matching the narrow-mode pattern. Removed the `title` attr on the workspace name span (was firing a native browser tooltip during drag).

`WorkspaceRow.test.tsx`'s `drag-steals-click guard` describe (its premise was the bug) replaced with `header pointer-down propagation (drag reachability)` covering name / chevron / Plus.

## [1.0.0-alpha.252] - 2026-04-29

### Fix(daemon): sweep:pid_dead broadcasts status=clear (#727 / closes #717)

Fix opencode (and any agent) leaving its SPA indicator stuck after Ctrl+C exit. Sweep was correctly detecting process death and clearing `agent_frames`, but the WebSocket broadcast carried `status=""` instead of `"clear"`, so SPA's `handleNormalizedEvent` (which keys the clear path on `status === 'clear'`) couldn't recognize the session was empty.

**Root cause** — `internal/module/agent/frame_ops.go` `buildProjectionNormalized` had asymmetric handling of two semantically equivalent "no top frame to display" states. The `projection == nil` branch passed through `result.Status` verbatim, while `projection != nil && TopFrame == nil` forced `StatusClear`. Sweep's `afterFrameCleared` (`sweep.go:551`) called the helper with `agentpkg.DeriveResult{}`, so the empty status leaked through to broadcast whenever a sweep cleared the last frame in a session.

**Round-1 fix** — pass `agentpkg.DeriveResult{Status: agentpkg.StatusClear}` explicitly at the sweep callsite. Surgical 1-line change with explicit caller intent.

**Round-2 hardening** (codex 3-parallel adversarial review):
- *Defense in depth*: `buildProjectionNormalized`'s `projection == nil` branch now defaults empty `Status` to `StatusClear` (fail-safe for future callers).
- *Race fix*: `afterFrameCleared` re-resolves `projectionForSession` right before broadcasting. If a hook handler raced in to create a new frame for the same session (e.g. user kills opencode and immediately runs `opencode` again, SessionStart hook lands mid-sweep), the broadcast now carries the live state instead of overwriting the just-installed running status with a stale clear. Best-effort — full serialization requires per-session locking, tracked separately.
- *Test hygiene*: split broadcast contract tests into a focused `internal/module/agent/sweep_broadcast_test.go` with a shared `readSweepNormalizedEvent` helper that decodes the `HostEvent` envelope and inner `NormalizedEvent` once.

#### Followups tracked

- #728 — Sweep broadcast may be silently dropped (lossy WS, no replay) — pre-existing architectural concern; #717's fix makes the consequence more visible but doesn't worsen it.
- #729 — Refactor: extract sweep broadcast emission into shared helper across `sweep.go:327` / `:499` / `:551`.

## [1.0.0-alpha.251] - 2026-04-29

### Feat(agent): W2 Phase 1 — schema + cc catalog naming separation + lifecycle metadata (#710)

Phase 1 of W2 (catalog naming separation) in the Lights system rebuild — see `docs/specs/2026-04-28-catalog-naming-separation-spec.md` for the full design and `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §1 for how W2 fits the larger rebuild.

**Goal**: separate three previously-conflated dimensions of hook events — Purdex's daemon-internal canonical name, the upstream agent's hook map key, and the lifecycle/frame-mutation effect — so daemon code uses Pdx-prefixed canonical names everywhere internally and only translates at the input/output boundaries (cc settings.json, codex hooks, opencode plugin).

**Phase scope**: cc end-to-end migration + shared schema + handler/frame_ops lifecycle metadata dispatch. codex / opencode catalogs remain on the legacy literal path via `isLegacyHookForUnmigrated` (Phase 2 / 3 migrate them); single user-facing alpha bump after all three phases land per spec §「W2 設計關鍵決議」#4.

#### Schema additions

- `HookEventSpec` gains `PurdexName` (Pdx-prefixed daemon-internal canonical), `UpstreamKeys []string` (upstream hook map keys), `Lifecycle LifecycleEventKind` (frame-mutation effect classification, 8 kinds). `Name` retained per plan G1 until PR-W2-cleanup-followup so legacy `~/.claude/settings.json` cleanup keeps working through the transition.
- `LookupByPurdexName` / `LookupByUpstreamKey` free functions provide spec §2.5's calling convention.
- `LifecycleEventKind` enum (`SessionStart` / `SessionEnd` / `UserPromptSubmit` / `Stop` / `StopFailure` / `SubagentStart` / `SubagentStop` / `None`) classifies frame-mutation effect independently of upstream naming.

#### CC end-to-end migration

- `EventRequest` / `hookPayload` JSON migrated to `purdex_name` (with `event_name` unmarshal alias for transition; Phase 3 removes the alias).
- cc 28 catalog entries populated with the three new fields (including unsupported / ignored entries — `Pdx`+original name with `LifecycleNone`).
- cc `DeriveStatus` + installer + cleanup/known sets all keyed by `PurdexName` / `UpstreamKey` via catalog-derived helpers; settings.json `command` trailing token now writes `pdx hook --agent cc PdxXxx` (matching the daemon-internal canonical), upstream hooks key remains `SessionStart` / `Notification` / etc.
- SPA cc fixtures keyed under `agent_type='cc'` migrated from upstream-key strings to PdxXxx; `useAgentStore` Notification-idle no-unread branch accepts both literals during the transition.
- SPA notification dispatch (`shouldNotify` informational suppression + `NotificationSettings.events` lookup + `buildNotificationContent` switch) normalizes the 4 user-facing PdxXxx names back to legacy at entry via `normalizeEventName`, so cc desktop notifications survive PdxXxx broadcasts (codex round-2 attack-side finding A-F01 — without normalization every cc permission prompt / Stop / StopFailure notification would silently drop).

#### Lifecycle metadata-driven dispatch

- `handler.handleEvent` error-guard, subagent transient-emit, and SessionStart subagent-reset paths key off `agentpkg.LifecycleEventKind` via `classifyLifecycle`'s spec §3.4.2 three-branch decision tree (catalog hit > legacy fallback > LifecycleNone). Catalog-hit `LifecycleNone` is a legitimate no-op classification; `LookupByPurdexName` + `isLegacyHookForUnmigrated` per-agent literal sets keep codex/opencode pre-migration traffic routing correctly.
- `frame_ops.applyFrameEvent` (detail-only / SessionEnd / SubagentStart-Stop / four SessionStart hot paths), `updateSubagents` / `mutateSubagents`, and `path_hint_extractor` migrated to lifecycle metadata. Transitional `matchesLifecycleName` / `normalizeLifecycleName` helpers added in P1-T8 and removed in P1-T11 / P1-T12 alongside their final callers.
- `isLegacyHookForUnmigrated` per-agent literal sets: codex 9 entries (includes Notification); opencode 8 entries (includes PermissionRequest, deliberately omits Notification — opencode's catalog has no Notification entry, accepting it would route an unknown event into the legacy fallback).

#### Cross-agent catalog-miss invariant tests (D-F01 / Q-F01 drift guard)

`TestHandleEvent_CodexPrematurePdxName_IsCatalogMiss` / `TestHandleEvent_OpencodeNotification_IsCatalogMiss` / `TestHandleEvent_CCPdxNotification_IsCatalogHitNotMiss` pin spec §3.4.2's three-branch boundary at the handler level so Phase 2/3 catalog migrations can't regress silently.

#### Codex review history

- **Spec / plan**: 5 codex review rounds on the spec, 1 on the plan; 5 plan-level findings (G1–G5) + 2 spec findings (S1–S2) all applied before implementation.
- **PR review round 1 (standard, job `bvl94kd52`)**: 1 P1 finding (R1-F1, cc legacy hook upgrade compatibility) **dismissed** per spec §「W2 設計關鍵決議」#4 + alpha-stage breaking-change posture from `kickoff_lights_rebuild.md` §「對齊已決議的設計關鍵點」#8.
- **PR review round 2 (3 parallel adversarial: attack `b4opavt0q` / defense `b0f8s7ahj` / file-quality `bhuqqc9i6`)**: A-F01 (SPA notification regression), Q-F03 (legacy_hook_test comment) fixed in this PR. D-F01 / Q-F01 (lifecycle classification overload) drift-guarded with new handler-level invariant tests; structural `EventResolution` refactor tracked as #722. Q-F02 (handleEvent SRP) tracked as #723. No critical / P1 findings outstanding.

### Closes

- Closes via review summary references throughout — no individual issue closes in this PR.
- Follow-up issues opened: #722 (EventResolution refactor for lifecycle classification overload), #723 (handleEvent SRP split, depends on #722).

## [1.0.0-alpha.250] - 2026-04-29

### Refactor(electron): retire runtime codesign — Stage 1b (#709) (#720)

Stage 1b of the macOS code-signing roadmap. Retires the runtime codesign concept (`detectSignedState` preflight + `resignAppBundle` helper) introduced in PR #672 / Stage 0. The runtime path was vestigial: macOS does not re-verify `CodeResources` post-launch on same-machine same-path relaunches, dev update is `PDX_DEV_MODE`-gated at three boundaries, and the OS never reads the signature record after first launch on a quarantine-cleared bundle.

Stage 0 protected against a SIGKILL caused by an unnecessary action; Stage 1b removes the action entirely (Option β).

- **`electron/updater.ts`** — delete `detectSignedState`, `resignAppBundle`, `getAppBundlePath`, `APP_ID`, `SignedState`, `PREFLIGHT_TIMEOUT_MS`, `NOT_SIGNED_PATTERN`, `stripAnsi`, the `__testing` namespace export, the `progress('signing')` call site, the now-stale `// no progress('restarting')` comment, the `node:child_process` import, and trim `path` import to `{ join }` (drop `dirname`). About 80 lines of code removed.
- **`electron/signing.test.ts`** — replace 3rd presence-checking static test with absence smoke; add 4 new static guards (progress-sequence with literal-array equality + total-call count, preload strict gate, daemon strict gate, SPA-absence). Delete entire `describe('updater signing preflight (runtime)', ...)` block (17 mock-driven tests covering deleted code) plus the helpers / `vi.mock` setup that supported it. File: 281 → 65 lines.
- **`spa/src/components/settings/DevEnvironmentSection.tsx`** — drop dead `signing` and `restarting` entries from `stepLabels`. `signing` was emitted by the deleted call site; `restarting` was always dead because `app.exit(0)` killed the process before any IPC could deliver. Daemon-rebuild flow's separate `daemonPhase === 'restarting'` render path is unaffected.
- **`electron/preload.ts`** — Round-2 attacker fix: tighten dev-update bridge gate from truthy ternary (`process.env.PDX_DEV_MODE ? ... : {}`) to strict `=== '1'`, matching the daemon's `os.Getenv("PDX_DEV_MODE") == "1"`. The truthy form would have exposed `applyUpdate`, `checkUpdate`, `streamCheck`, `onUpdateProgress` for `PDX_DEV_MODE=0`/`false`/`no` — values commonly used to mean "off". Pre-existing bug from the original dev-update PR; codified contract via the new strict-equality preload-gate guard.
- **`electron/main.ts`** — Round-2 defender fix: wrap `dev:*` `ipcMain.handle(...)` registrations in `if (process.env.PDX_DEV_MODE === '1')` block. Closes the spec §7 R7 "main-process boundary half-open" residual surface — now all three layers (preload + main + daemon) refuse dev update with the same strict gate.

### Test gate

39 → 27 tests, all green: `signing.test.ts` 20 → 8 (2 existing static + 1 absence smoke + 1 progress-sequence + 1 preload-gate + 1 daemon-gate + 1 SPA-absence + 1 main-gate); `keybindings.test.ts` 19 unchanged.

### Live verification

Manual Air verification (spec §8.2 unsigned bundle / §8.3 ad-hoc signed bundle) is the canonical Stage 1b safety gate. The `--verify --deep --strict` post-update failure on signed bundles is the **expected outcome** documented in §3.4 — the Stage 1b safety claim is "relaunch succeeds on same machine same path", not "signature stays cryptographically intact for redistribution".

### Closes

Closes #712 (darwin integration test for Stage 0 preflight — preflight retired, nothing to integration-test) and #713 (`APP_ID` constant drift across `updater.ts`/`package.json`/`build-electron.mjs` — `APP_ID` deleted from `updater.ts`).

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| Spec review (`task-moivfw7n-a8x1ph`) | 8 (2 P1 + 4 P2 + 2 P3) — narrow §3.4 claim scope, ad-hoc signed bundle Stage-1b gate, PDX_DEV_MODE precision, progress guard, R2 split, bump separation, SPA dangling | All addressed in spec v1.1 before plan |
| Plan review (`task-moiw8665-6ak7i6`) | 11 (3 P1 + 5 P2 + 3 P3) — preload/daemon gate static asserts, ordered array equality, T2 tsc gate placement, dirname trim, vitest imports, Run A/B independent installs, SPA grep gate, Closes syntax, T2 naming | All addressed in spec v1.2 + plan v1.1 before implementation |
| R1 standard | 0 actionable defects | Approve |
| R2 attacker (`review-moiyv5n5-eqxwcr`) | 1 P1 — preload truthy gate vs daemon strict gate | Fixed `fcb78091` |
| R2 defender (`review-moiyz7eb-l7hofd`) | 3 (D1 manual gate / D2 main-process boundary half-closed / D3 progress guard imprecise) | D2/D3 fixed `9f20bcb1`; D1 manual verification deferred to Air post-merge |
| R2 file-health (`review-moiyzrod-r7x9j2`) | 3 (F1 test SRP drift / F2 doc internal staleness / F3 dead `restarting` label) | F2/F3 fixed `9f20bcb1`; F1 deferred (rename ripple too wide) |

### Known follow-up

- **F1 (medium)** — `signing.test.ts` now owns gate contracts that exceed the "signing configuration" naming. Defer rename/split until file-health concern accumulates.
- **Air manual verification** — Run A (unsigned) + Run B (ad-hoc signed) per spec §8.2/§8.3 / plan §5. Post-merge to be executed; relaunch failure on either run is a rollback trigger.

## [1.0.0-alpha.249] - 2026-04-29

### Fix(opencode): plugin emit() stdin TypeError hotfix (#715) (#716)

Pre-existing bug since commit `ffdd4e14` (2026-04-21, initial opencode integration). The rendered opencode plugin's `emit()` helper passed a raw JSON string as `Bun.spawn`'s `stdin` field, which Bun rejects with `TypeError: ERR_INVALID_ARG_TYPE`. Every opencode hook event has thrown for 7 days; `agent_events.db` had zero `agent_type='opencode'` rows in production. Existing template tests only diffed rendered text and never exercised a real Bun runtime, so CI silently passed.

- **Daemon `internal/agent/opencode/plugin_template.go`** — pull JSON encoding before `Bun.spawn` (`const encoded = JSON.stringify(payload)`), switch the `stdin` field to `'pipe'`, and write the payload through the FileSink lifecycle (`proc.stdin.write(encoded); proc.stdin.end(); await proc.exited`). Pre-encoding restores the pre-fix semantic where a serialization failure prevents the spawn entirely (rather than leaving the spawned `pdx hook` blocked on stdin EOF). The rendered body is byte-different from the broken era, so `CheckHooks` reports drift on pre-fix managed plugins and the next `pdx setup --agent opencode` writes the fixed body.
- **Test `internal/agent/opencode/plugin_template_bun_integration_test.go`** (new) — real-Bun integration test that renders the plugin against a stub `pdx` shell binary, runs the result with `bun <plugin.mjs>`, and asserts the stub captured the JSON payload on stdin. Failure-mode classification distinguishes the pre-fix `ERR_INVALID_ARG_TYPE` red signal from harness failures (envelope mismatch, deadlock, syntax error). Four-layer skip gates: Windows / missing `/bin/sh` / no `bun` on `PATH` / `bun --version` failure — each is a `t.Skip` not a fail, so a single `go test ./...` run works across CI environments.
- **Test `internal/agent/opencode/hooks_test.go`** — append `TestCheckHooks_PreFixManagedBodyReportsDrift` that synthesizes the actual pre-fix body (`stdin: JSON.stringify(payload)` with no `encoded` const, no `write`/`end` follow-up lines), writes it under HOME, and asserts `CheckHooks` reports drift on at least one event before `InstallHooks` returns the directory to the canonical state. Lifts spec AC3 from manual mlab observation into a CI-enforced unit assertion. `RenderManagedPluginForTesting` exposed via `hooks_export_test.go` (mirroring the existing `SetResolveCanonicalPdxPathForTesting` export pattern).

### Live verification

mlab on alpha.248 + spawn-fix branch captured 20 opencode events end-to-end: 4 `SessionStart`, 6 `UserPromptSubmit`, 5 `Stop`, 2 `SubagentStart`, 2 `SubagentStop`. All `latest_decision=broadcasted`, all `terminal_status=completed`. Pre-fix DB had **zero** opencode rows for the entire 7-day broken window.

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| Spec review (`task-moiu936r-x7a8nh`) | 7 (5 P2 + 2 P3) — stdin types precision, await semantics note, `bun <script>` vs `bun run`, `bun --version` probe, Windows skip-gate, CheckHooks drift acceptance, acceptance-criteria scope | All addressed in spec v1.1 before plan |
| Plan review (`task-moiufay4-0c5pdm`) | 6 (3 P2 + 3 P3) — TDD red precision, single-execution `CombinedOutput`, `.mjs` vs `.js` ESM, `/bin/sh` gate, AC3 in-repo unit test, DOD program/process split | All addressed in plan v1.1 before implementation |
| R1 standard (`review-moiuppm6-63g2f2`) | 0 blockers | Approve |
| R2 3-parallel (attack + defense + file-quality) | 2 medium (F1 0.78 partial-failure orphan subprocess; F2 0.87 drift fixture impossible hybrid body) + 1 approve | Both addressed in commit `147bd46c` |

### Known follow-up (not blocking #715)

- **#717** — opencode session indicator stays active after Ctrl+C exit because upstream opencode (anomalyco/opencode#10524) intercepts SIGINT/SIGTERM before plugin handlers run. `Stop` events fire correctly per prompt, but no `SessionEnd` ever fires — daemon-side liveness probe / heartbeat needed. Tracked separately.

## [1.0.0-alpha.248] - 2026-04-28

### Fix(electron): unsigned-aware preflight in dev update resign (#709 Stage 0) (#711)

Stage 0 of the macOS code signing roadmap (#709). Fixes the dev update regression introduced by PR #672 (alpha.234) where `applyUpdate` unconditionally re-signed the running bundle and triggered macOS AMFI to SIGKILL the process before `app.relaunch()` could run — symptom: SPA progress reaches `applying` then disappears, app does not relaunch, manual restart shows old version. Reproduces on **unsigned** `Purdex.app` (the actual user deployment — `codesign -dv` reports `code object is not signed at all`).

- **Electron `electron/updater.ts`** — `resignAppBundle()` gains a three-state preflight via new `detectSignedState()` helper. `codesign -dv` exit 0 → `signed` (re-sign + verify, unchanged); non-zero with normalised stdout/stderr matching `/code object\s+is\s+not\s+signed\s+at\s+all/i` → `unsigned` (skip codesign, the actual fix); any other outcome (status null / spawnSync error / unrelated stderr) → `unknown` (throw with actionable error including bundle path + `PDX_SKIP_MAC_SIGN=1` remediation hint, routed through existing `applyUpdate` rollback). `PREFLIGHT_TIMEOUT_MS = 10_000` prevents codesign hang from blocking the main process. Detection normalises ANSI escapes + merges stdout/stderr so case differences, ANSI wrapping, stdout-borne phrasing, and irregular whitespace don't cause false-negatives. Imports unified to `node:child_process` so tests can `vi.mock('node:child_process')`. `__testing` namespace export gates `detectSignedState` + `resignAppBundle` for unit tests (Stage 1 will retire this when bundle-swap refactor lands).
- **Test `electron/signing.test.ts`** — restructured into static block (3 grep tests for package.json + build-electron + updater contract markers) and runtime block (17 tests under `vi.mock('node:child_process')` + `vi.mock('electron')`): 5 detection core cases, 5 resilience cases (case difference / ANSI wrap / stdout-borne / irregular whitespace / timeout), 1 timeout-option assertion, 6 `resignAppBundle` dispatch cases (PDX_SKIP_MAC_SIGN / non-darwin / unsigned-skip / unknown-throw + actionable / ad-hoc identity / forced identity). `mockCodesign()` + `loadTesting()` helpers eliminate boilerplate. `process.platform` mock preserves descriptor flags via `getOwnPropertyDescriptor` + `afterEach` restore (vs. `vi.stubGlobal` which would replace the entire process object).

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| Spec review | 9 (4 P1 + 4 P2 + 1 P3) — three-state detection contract gap, mock strategy, identity precedence, etc. | All addressed in spec v1.1 before plan |
| Plan review | 8 (4 P1 + 4 P2) — verification toolchain availability, T2 RED contract, `process.platform` descriptor restore, bump base protocol | All addressed in plan v1.1 before implementation |
| R1 standard | 0 blockers (sandbox couldn't run vitest due to read-only filesystem; static analysis clean) | — |
| R2 4-parallel (adversarial + attacker + defender + file-health) | 12 (2 P1 + 4 P2 + 6 P3) — stderr brittleness, no spawnSync timeout, runtime test boilerplate, plan docs RED count inconsistency, error message actionability, `__testing` namespace style, `applyUpdate` SRP, APP_ID drift | All P1 + must-fix P2 fixed in commit `17b81143`; F6/F9 → issues #712/#713; F7/F8 deferred to Stage 1 (#709) |

### Plan deviations

1. T2 RED count: plan v1.0 predicted 11 RED runtime tests; actual was 4 because the stub's hardcoded `'unknown'` coincidentally satisfied 3 detection assertions and the unmodified `resignAppBundle` satisfied 4 dispatch assertions. The 4 actual REDs were exactly the new contract paths (`signed`/`unsigned` classification + unsigned-skip + unknown-throw); plan v1.1 documented this.
2. R2 stderr matching expanded beyond plan's exact-substring shape — added ANSI strip + normalised regex per adversarial F1.

### Out-of-scope (tracked under #709)

- Stage 1: dev update bundle-swap refactor + entitlements + Hardened Runtime config
- Stage 2: self-signed cert flow + cross-machine trust docs
- Stage 3: Apple Developer ID + notarization in CI release pipeline

### Followup

- `#712` — Stage 1+: darwin integration test for real codesign -dv (close stderr-format-drift gap)
- `#713` — refactor(electron): consolidate APP_ID across updater.ts / package.json / build-electron.mjs

## [1.0.0-alpha.247] - 2026-04-28

### Feat(spa+daemon): file-not-found popup with three-layer fallback (P5) (#707)

Editor P5 — final phase of the **Editor self-contained** series. Clicking a non-existent file path (terminal link or FileTreeView) no longer silent-fails: the new pipeline runs `stat → Layer-1 path-cache lookup → popup` and the popup's expand action triggers Layer-2 (session cwd) + Layer-3 (workspace projectPath) `fs.search` against a server-side capability allowlist.

- **Daemon `internal/module/fs/search_engine.go`** — pure `Search(ctx, req)` with mandatory dir excludes (`node_modules`, `.git`, `.cache`, `dist`, `.pnpm-store`, `.next`, `.turbo`) and basename excludes (`*.lock`, `*.log`) UNIONed with client filters; `respectGitignore *bool` nil → true; gitignore parse failure returns 4xx (no fail-open); symlink loop avoidance; depth via `filepath.Rel`. In-house `preflightGitignore` + `bracketsBalanced` because `go-gitignore` silently drops bad lines.
- **Daemon `internal/module/fs/search_handler.go`** — `(m *FsModule) handleSearch` capability-only roots: `{kind:"session-cwd", sessionCode}` resolves via `SessionProvider.GetSession(code).Cwd` and is **EvalSymlinks'd** so symlink hops can't bypass the system-path allowlist (R2 high — a `/tmp/ws/home-link/proj` symlink to `$HOME` would otherwise let WalkDir scan the user's home tree). Both lexical and resolved forms gate; `kind:"absolute"` → 4xx; `workspace-projectPath` → 501 (defer); system paths `/`, `/etc`, `/sys`, `$HOME` direct, `/Users` direct, `/Volumes` rejected.
- **SPA `spa/src/lib/file-open/`** — host-bound `createOpenFileService({fsBackendFactory, popupController, tabOpener})` with strict ENOENT/404 error classification; `fsBackendFactory(hostId)` resolves once per call so workspace/host switch mid-flight can't corrupt the open. `fsSearchByCapability(hostId, basename, roots, limits?, signal?)` threads an AbortSignal into fetch so popup dismiss tears down the daemon-side WalkDir (R2 medium). `FileNotFoundError` re-thrown by tryOpenFile when `popupOnMissingFile` is off.
- **SPA `file-not-found-popup-service.tsx`** — singleton mount with `import.meta.hot.dispose(hideFileNotFoundPopup)` and `AbortController` cancellation; controlled re-render reuses the live root + token instead of aborting (R2 medium — previously layer-2 results arriving first would abort the layer-3 fetch via the popup token).
- **SPA `file-open-bootstrap.ts`** — module-level `mergedHits` accumulator so layer-2 + layer-3 results merge into the same expanded popup; reset on fresh ask-expand / layer1-multi mounts. Exports `openFileAsBufferDirect` for the tilde fallback when home resolve fails (R3 P2 — daemon rejects `stat('~/foo')` with 400, classified as auth/network not ENOENT, so the missing-file pipeline can't open a blank buffer).
- **SPA `EditorOpenBehaviorSection`** — two new toggles in Editor settings (per `feedback_core_vs_module_settings`, `useEditorSettingsStore` not `useUISettingsStore`): `popupOnMissingFile` (default `true`) master gate; `autoSearchLayer1` (default `true`) cache auto-lookup.
- **Terminal link / FileTreeView integration** — both consumers route through `tryOpenFile`; the previous `getDefaultOpener + openSingletonTab` direct calls move into the bootstrap-time `tabOpener`. Click-handler context: `FileNotFoundError` → `console.warn` (expected when popup off); auth/network/host removed → `console.error` so console-watching surfaces broken transport instead of silently dropping clicks (R1 P2 + R4 P2 symmetric fix in FileTreeView).
- **Privacy** — fs.search payload sends only basename + capability roots; daemon resolves the capability and never echoes the resolved path beyond what the user already sees in the popup CTAs.

### Review history

| Round | Findings | Outcome |
|-------|----------|---------|
| R1 standard | 1 P1 (daemon stat 404 lost status — popup pipeline never triggered for daemon backend) + 1 P2 (terminal-link catch swallowed every error including auth) | Both fixed |
| R2 adversarial | 1 high (symlink session cwd bypassed allowlist) + 1 medium (popup dismiss didn't cancel fetch; show() replacement aborted peer searches) | Both fixed; mergedHits accumulator added |
| R3 standard | 1 P2 (unresolved tilde regression — `~/foo` → daemon 400 instead of new buffer) | Fixed; `openAsBuffer` direct-open dep added |
| R4 standard | 1 P2 (FileTreeView voided promise — unhandled rejections from auth/popup-off) | Fixed; FileTreeView mirrors terminal-link error split |

### Plan deviations

1. Path cache keying — actual `usePathCacheStore` is keyed by `(hostId, cwd)` per the P4 redesign, not workspaceId. `OpenFileContext` carries `cwd` (captured at click time) for the cache and `sourceWorkspaceId` only for Layer-3.
2. Settings store moved from `useUISettingsStore` to `useEditorSettingsStore` per `feedback_core_vs_module_settings`.
3. Layer-3 501 silently treated as not-implemented (no surface error).
4. `go-gitignore` library silently drops bad lines; in-house preflight catches the unbalanced `[...]` case the plan tests against.

### Followup

- closes #703 — `usePathCacheStore.lookup` / `pruneStaleCandidate` now have a production consumer.
- daemon `workspace-projectPath` capability — needs workspace registry; deferred.
- bare-filename Editor-disabled UX gap from P3 still open.

## [1.0.0-alpha.246] - 2026-04-28

### Feat(spa): Quick Commands v2 Phase 1c — HOST_ACTIONS entry with host liveness probe (#705)

Phase 1c of the Quick Commands v2 capability/binding/slot system — adds a chip slot beside the new-session button on the host detail page (`SessionsSection`). Users who bind a command to mount = HOST in Settings now see chips next to the `+ New Session` button; clicking creates a tmux session on that host (cwd = host default `~`), sends the bound command, and switches to the new session.

- **`runHostSlot` / `HostSlotContext`** — new sibling executor to `runWorkspaceSlot`. `HostSlotContext.hostId` is required non-null string (host detail page owns it via prop). `HostDeps` carries `switchToSession` + `assertHostLive`; deliberately omits `assertContextLive` and `resolveHostId` (workspace probes / picker callback) so cross-shape pollution fails `tsc -b`. Failure UX matches `runWorkspaceSlot`'s precedence rules per spec §3.3.
- **Host liveness probe — three call sites for defense in depth** (R2 of codex PR review):
  1. **Pre-create** — at `runHostSlot` entry, before `createSession`. Catches stale chip clicks (host already deleted at click time, before chip re-rendered disabled). Without this gate, `useHostStore.getDaemonBase` falls back to `activeHostId ?? hostOrder[0]` and creates an orphan session on the wrong host.
  2. **Post-create** — after `createSession` resolves, before `executeCommand`. Catches the during-await race (host deleted while createSession Promise was in flight). Stops destructive commands from shipping.
  3. **Retry** — inside the toast retry action. Catches the between-toast-and-retry window.
- **Workspace-aware switch** (R1 of codex PR review): `switchToSession` snapshots the host page's owning workspace at click time via `findWorkspaceByTab(activeTabId)` BEFORE any await, then closes over the captured `owningWsId`. Standalone host pages get explicit `null` (bypasses `insertTab`'s `activeWorkspaceId` fallback so a stale or post-await `activeWorkspaceId` can't redirect the new session).
- **Executor-level busy guard** — `executingRef + executing` state mirrors workspace popover pattern; suppresses fast double-click between React event ticks.
- **Disconnect handling** — chip is `busy={isOffline || executing}`, matching new-session / row actions disable semantics.
- **`<QuickCommandMenu>` row removal** — removed from `SessionsSection` rows (consolidated to new-session entry); v1 component itself stays for `PaneLayoutRenderer`.
- **Empty `session.code` handling** — server returning blank/whitespace `code` is treated as create failure (avoids confusing 404 from downstream `executeCommand`).
- **Spec / plan / JSDoc** — three-site `assertHostLive` contract documented in spec §3.3.1, plan §1c.0 design block, and `HostDeps.assertHostLive` JSDoc. Tests use `mockReturnValueOnce` sequences to model phase-specific probe states; assertions favor `>= 1` over exact counts to avoid false-fails when probe timing changes.
- **Review history** — codex plan-review (`task-moijtc8w-w8rf8w`, 6 findings) → R1 standard PR review (1 P2: click-time snapshot race, fixed `4a54af18`) → R2 adversarial (1 medium: pre-create gate missing, fixed `0103c942`) → R3 verification (1 low: docs not aligned with three-site contract, fixed `976c83c2`) → R4 final approve.

### Followup issues

- #689 — server-side orphan session cleanup (Phase 2)
- #695 — ESLint custom rule restricting `runWorkspaceSlot` / `runHostSlot` parameter shape escape hatches (`as any`, spread, `Object.assign` cast bypass)

## [1.0.0-alpha.245] - 2026-04-28

### Feat(daemon+spa): pathhint v1 channel + spa path cache (P4) (#696)

Editor P4 — daemon emits `agent.path_hint` events when CC PreToolUse / PostToolUse touches a file, and the SPA caches recent dirs per `(hostId, cwd)` so future P5 fuzzy lookups can resolve relative paths without round-tripping through the user. Cache scope is keyed by the agent's working directory (the real boundary), with `sessionCode` carried as a per-entry tag so lookups can sort same-session entries first. This survives CC restarts in the same repo (a common workflow when the context window fills up) and lets a Codex session in the same cwd benefit from CC's earlier hints, without crossing into unrelated repos in the same workspace.

- **Daemon** — `agent.path_hint` v1 schema (`{ schemaVersion, agentId, sessionCode, cwd, dir, kind, timestamp }`) with 200-entry ring buffer; pure `ExtractPathHint` with cwd sourced from CC raw_event and `SessionInfo.Cwd` fallback; payload defenses (≤64 KiB raw event, ≤4 KiB file_path / cwd, no NUL/control chars); `normalizeCwd` preserves `/` so agents at root don't lose their cwd; `PathHintDedupCache` keyed by `(session, dir, basename)` to prevent 5s blackout after SPA prune; `EmitPathHint` wired into CC PreToolUse / PostToolUse right after verify accepts.
- **SPA** — `usePathCacheStore` keyed by `(hostId, cwd)` with NUL-separator scope keys (so colon-bearing hostIds don't collide), 50-entry LRU per scope, and `lookup(currentSessionCode?)` priority of same-session-first then recency. `clearBySession(hostId, code)` is host-scoped per R3 P2 so cross-host sessionCode collisions don't wipe live entries. Persisted via `purdexStorage` with rehydrate sanitizer that replays the same invariants `add()` enforces (R2 file-quality F1). Intentionally NOT registered with `syncManager` — cache is per-window-owner; per-origin localStorage still survives tear-off.
- **Dispatch** — `lib/agent-ws/` split into router + per-event-type handlers; `path-hint-dispatch.ts` validates `event.session === payload.sessionCode` (R2-D3), rejects payloads >64 KiB (R2-A2), drops on schema/cwd/dir/kind invariants. `AGENT_WS_EVENT_TYPES` SSoT (R2-F2) consumed by both router and `useMultiHostEventWs`.
- **Privacy** — dir-level only; no full file path or basename in broadcast payload. HostId travels in the broadcast envelope.
- **Review history** — R1 standard (2 P2 fixed) → R2 attacker / defender / file-quality (5 high/medium across the three lanes) → scope redesign `(host, workspace) → (host, cwd)` per user feedback (workspaces are free-form groupings; cwd is the real boundary; sessionCode demoted to per-entry priority tag) → R3 standard (1 P2 cross-host sessionCode collision + 1 P3 root cwd `/` collapsed by TrimRight, both fixed) → R4 standard (1 P2 NotebookEdit `notebook_path` field fix; remaining "no consumer" finding tracked as #703).

### Followup issues

- #703 — `usePathCacheStore.lookup` / `pruneStaleCandidate` need a production consumer; P5 (NewTabPage popup-mount + `fs.search` backend allowlist) is the planned wirepoint, plus the bare-filename Editor-disabled UX gap from P3.

## [1.0.0-alpha.244] - 2026-04-28

### Feat(spa): Quick Commands v2 Phase 1b' — Plus hover popover (WORKSPACE_ACTIONS) (#701)

Quick Commands v2 Phase 1b' — adds the Plus-button hover popover entry-point for `WORKSPACE_ACTIONS` chips on each `WorkspaceRow` in the sidebar, with mobile/touch long-press fallback. Marked transitional and packaged as a stand-alone PR so it can be reverted/replaced later without touching the stable Phase 1b foundation (data layer / Settings UI / context menu).

- Desktop: hover (or keyboard focus) on Plus → chip popover unfolds left of the button. Mouseleave / blur the wrapper → popover collapses.
- Mobile/touch: long-press (≥500ms) on Plus → popover latches open; release-then-tap a chip executes. Short tap (<500ms) → original add-tab behavior unchanged. Tapping outside the hub closes the popover.
- `HostPickerPopover` portal'd to `document.body` so the picker escapes the transformed wrapper subtree and isn't torn down by hub blur events. Sync `onPickerOpenChange` notification from `resolveHostId` / `settlePicker` (not `useEffect`) so the hub's `pickerOpenRef` updates BEFORE child auto-focus fires blur on the chip.
- Wires `runWorkspaceSlot` with the #690 `assertContextLive` enforcement (alpha.242); passes a workspace-liveness probe for fail-closed during async `createSession`.
- `switchToSession` carries the canonical pre-check / read-back / rollback transaction so workspace-deletion mid-`executeCommand` leaves no orphan tab + no active-tab/workspace mutation.
- Long-press click suppressor is now one-shot — cleared inline in `onClick` and via `popoverOpen=false` effect, so post-touch mouse/keyboard activation isn't silently dropped on hybrid devices.
- Four rounds of codex review collapsed to zero findings: R1 standard (3 P2), R2 3-parallel attacker/defender/file-quality (5 medium-high), R3 verification (1 medium portal+focus race), R4 final approve. Twelve regression tests added across `WorkspaceQuickActionsPopover.test.tsx` + `WorkspaceRow.test.tsx`.

### Followup issues

- #689 — server-side orphan session cleanup (Phase 2)
- #695 — ESLint custom rule limiting `runWorkspaceSlot` / `runHostSlot` second/third arg cast/spread escape hatches

## [1.0.0-alpha.243] - 2026-04-28

### Docs(specs): hook→status→燈號 audit (W1, fix-spec PR-2) (#692)

Lights rebuild W1 — the canonical 577-line audit (`docs/specs/2026-04-28-hook-status-audit-spec.md`) for cc / codex / opencode that becomes the SOT for W3-W7. Drives §6 W5 燈號 bug 修復清單 (8 items) and §7 W6 ad-hoc ProbeIntent gap 清單 (6 items), and disciplines spec drift via §7.1 design constraints.

- §6 / §7 are the canonical work pools — §4 三家 agent 矩陣 only short-references them.
- §7.1 prevents the framework drift that triggered the fix-spec rewrite: probes go through `ProbeIntentProvider`, not synthetic hook events; no always-on probe; no generic `ProbeProfileProvider`; detectors live in `internal/agent/{cc,codex,opencode}/probe_intent_*.go`.
- §7.2 picks W6-3 (codex error) as the first ProbeIntent so the interface gets finalised against a real, lazy-shaped need.
- Two-round codex review converged with all findings addressed (R1 standard + R2 defender / spec-alignment per `feedback_codex_pr_review_spec_alignment`).
- Follow-up issues filed: #698 (daemon restart 後 activeWatchers 不恢復, W4/W6 platform prerequisite) + #699 (audit doc 拆檔評估 after W3-W6 ship).
- Pure docs change. Unblocks PR-3 W2 catalog naming separation (W1-independent) and PR-4 W3+W4 framework 撤回 + observability.

## [1.0.0-alpha.242] - 2026-04-28

### Feat(spa): enforce assertContextLive on runWorkspaceSlot Deps (#690) (#694)

Quick Commands v2 — type-level + runtime fail-closed defense in depth for the round-4 destructive-command guard introduced in PR #686. Future workspace-context Slot callers (Phase 1b' Plus hover popover is the first) can no longer silently regress the guard by omitting `assertContextLive`, and Phase 1c HOST_ACTIONS is now type-locked into a sibling `runHostSlot` rather than reusing the workspace executor.

- `runWorkspaceSlot.Deps.assertContextLive` is type-level required (was optional). Single production caller already wires it; no caller change needed for the 1b path.
- New `WorkspaceSlotContext = SlotContext & { workspaceId: string }`; `runWorkspaceSlot(ctx)` requires it, so `{ hostId }` + dummy probe can no longer slip through (round-2 D1).
- Runtime guard hardened to fail-closed: `typeof === 'function'` + try/catch + strict bool check. Cast-bypassed or throwing probes now route to the `switch_failed` toast path instead of unhandled-rejecting after `createSession` (round-2 A2).
- Type-level invariants verified by `tsc -b` (run via `pnpm run build`), not vitest. Conditional-type assertions (`IsAny` + `Pick`-required + null-rejection) replace the round-1 `@ts-expect-error` directive that could be silently consumed by unrelated `Deps` growth.
- Spec §3.3.1 + master plan superseded notes added so Phase 1b' / 1c implementers see the enforcement contract before copying historical examples.
- Three rounds of codex review converged: spec+plan pre-impl review (5 findings) → R1 standard (0) → R2 attacker / defender / file-quality (5 medium — D1/A1/A2/F1 fixed; A3 → issue #695 for ESLint custom rule) → R3 verification (1 high — branch base lagged main, rebased). Followup #695 (ESLint enforcement against type cast escape hatches) deferred as scope > #690.

## [1.0.0-alpha.241] - 2026-04-28

### Refactor(spa): migrate file path link detection settings to editor (P3) (#688)

Editor P3 — file-path link detection (the terminal feature that turns `path/to/file.ts` and `path/to/file.ts:42:7` into clickable links that open in the editor) is now scoped to the Editor module instead of bleeding into Terminal Settings. When Editor is disabled, the matchers also stop detecting, eliminating the prior gap where settings were hidden but matchers still ran.

- New `EditorLinkDetectionSection` component owns the file-path link detection toggles; Terminal Settings keeps only terminal-native link types.
- i18n keys for file-path link detection moved from `terminal.*` to `editor.*` scope.
- New `fileMatchersEnabled` flag gates all four file-path matchers (basename + path with/without line/column) on Editor module enablement; renamed from the round-1 `editorFilePathMatchersEnabled` after the defender / file-quality reviewers independently flagged the bare-filename matcher having the same boundary.
- HMR dispose path now calls `__resetBuiltinTerminalLinks` so a hot reload doesn't leak stale matcher registrations.
- Three rounds of codex review converged: R1 standard (1 P2 — settings hide but matchers still detect) → R2 attacker / defender / file-quality (2 medium — bare matcher boundary + HMR reset) → R3 approve (no actionable correctness issues). Known minor UX gap: bare-filename toggle remains visible in Terminal Settings when Editor is disabled, but runtime no longer detects (setting-without-effect; deferred polish).

## [1.0.0-alpha.240] - 2026-04-28

### Feat(spa): quick-commands v2 — Phase 1b (Settings UI + executor + workspace context menu) (#686)

Phase 1b of Quick Commands v2 — the first user-visible mount point for the v2 capability/binding/slot model. Settings UI lets users create / edit / delete commands and bind them to slots; right-clicking a workspace in the sidebar shows the bound `WORKSPACE_ACTIONS` chips and runs them through `slot-executor` (create session → optional host picker → send keys → switch focus). Five rounds of codex review converged.

- `inferWorkspaceHostId` — majority-vote host resolution from a workspace's tmux-session tabs (spec §3.2.1) with deterministic tie-break; returns null when the workspace has no tmux-session tabs so callers can open `HostPickerPopover`.
- `<HostPickerPopover>` — reusable host picker honouring spec §3.2.2 lifecycle contract (resolves to null on unmount / outside-click / duplicate-resolve).
- `<CommandSlot>` — shared component bridging bindings → bound capabilities → executor; `mountTo` / `hostId` / `busy` / optional custom `render` props.
- `slot-executor` — three-stage failure UX (create-session / send-keys / switch) per spec §3.3 with toast-precedence decision matrix; `assertContextLive` callback blocks `executeCommand` if the workspace was deleted while `createSession` was in flight (codex round-4 — destructive commands must not ship to a session whose context is gone).
- `useUndoToast` schema — optional `action` / `actionLabel` so create-session and switch failures can omit the action button while send-keys failure carries Retry (spec §3.3 / codex round-1 B4).
- `QuickCommandsSettingsSection` — list + edit dialog + multi-select mount chips with arrow-key roving focus (codex round-1 C15) and a11y (focus trap / Esc / aria).
- `WorkspaceQuickCommandsContextMenu` — mounts `<CommandSlot mountTo=WORKSPACE_ACTIONS>` on `WorkspaceRow` right-click; transactional `switchToSession` with pre-check, read-back, and rollback (closeTab + restore prevActiveTabId) so a deleted-workspace race leaves no orphan tab and no dangling active-tab mutation. Inherits `cwd` from `workspace.moduleConfig.files.projectPath` (spec §3.2).
- `quick-command-bindings.ts` consumers (CommandSlot / WorkspaceContextMenu / WorkspaceQuickCommandsContextMenu / QuickCommandsSettingsSection) all route binding lookups through `getBindingTargets` so capability ids colliding with inherited Object.prototype methods (`toString` / `valueOf` / etc.) can't crash the slot host.
- Five rounds of codex review converged: R1 standard (2 P2) → R2 attacker / defender / file-quality (4 findings) → R3 transaction layer B (orphan tab) → R4 transaction layer C (destructive command) → R5 final approve. 13 findings closed; 2 follow-ups tracked: #689 (server-side orphan session cleanup) and #690 (assertContextLive wiring enforcement).

## [1.0.0-alpha.239] - 2026-04-28

### Feat(spa): tabs cluster by kind on insert (P2) (#684)

Editor P2 — tab insertion no longer appends every new file to the very end of the workspace tab strip. When a file kind already exists to the right of the active tab, the new tab clusters next to it instead.

- New `findInsertTarget` predicate-based helper (generalized from the previous browser-only `findBrowserInsertTarget`).
- New `computeClusterInsertTarget` helper anchors on `ws.activeTabId` (workspace-scoped) so async terminal-link opens that race a workspace switch can't return an `afterTabId` belonging to another workspace.
- New `openClusteredTab` helper guarantees the same `afterTabId` is forwarded to BOTH `useTabStore.openSingletonTab` and `useWorkspaceStore.insertTab` — the TabBar renders from `workspace.tabs`, so the two stores must agree on placement.
- Three call sites adopt the predicate: `open-browser-tab` (browser-only, refactored), `terminal-link` file-path opener, `FileTreeWorkspaceView`. Editor / image-preview / pdf-preview tabs cluster together; sessions and browser tabs are unaffected.
- Three rounds of codex review: round-1 caught a tabOrder vs workspace.tabs divergence; round-2 (3-parallel attacker / defender / file-quality) independently flagged a cross-workspace `activeTabId` race; round-3 standard review saw no introduced bugs. Follow-up #685 tracks an intentional gap (existing singleton file tabs not auto-repositioning on re-open).

## [1.0.0-alpha.238] - 2026-04-28

### Refactor(spa): extract quick-command-bindings module + split store tests (#682)

Closes #679 (Q2 + Q3) — codex round-2 followups deferred from PR #677. Pure-data primitives (`QuickCommand` / `Bindings` / `QuickCommandData` / `UNSAFE_KEYS` / `sanitizeBindings` / `getBindingTargets` / `mergePersistedQuickCommandState`) move out of the store into a standalone module.

- New `spa/src/lib/quick-command-bindings.ts` — schema, sanitizer, own-property guard, persist merge function. `useQuickCommandStore` reduces to state + actions + persist + sync glue and imports from the pure module. Sync contributor + its test import `sanitizeBindings` and `QuickCommand` directly from the pure module instead of pulling them through the store.
- `mergePersistedQuickCommandState` is now generic over `T extends QuickCommandData` so the store can pass its full state (data + actions) and recover the same shape — action methods survive the merge. New test asserts this contract.
- 366-line `useQuickCommandStore.test.ts` split into three focused files: `lib/quick-command-bindings.test.ts` (sanitizer + own-property guard + hydrate boundary), `stores/useQuickCommandStore.crud.test.ts` (capability CRUD), `stores/useQuickCommandStore.bindings.test.ts` (store-level binding integration + null-hostId + prototype-key safety).
- Two rounds of codex review approved with no findings.

## [1.0.0-alpha.237] - 2026-04-28

### Feat(spa): editor module owns file openers + disabled placeholder (P1) (#675)

Editor module is now self-contained: it declares its three file openers (Image / PDF / Text) instead of having `register-modules.tsx` register them globally. When Editor is disabled, panes show a `DisabledModulePlaceholder` with reload-required hint instead of silently rendering nothing.

- New `ModuleDefinition.fileOpeners` field; `applyModuleFileOpeners()` helper applies them owner-scoped.
- `file-opener-registry` switched to nested `Map<owner, Map<id, opener>>` to avoid cross-module key collisions, with `clearFileOpenersForOwner()` HMR-safe disposal.
- New `DisabledModulePlaceholder` component + i18n keys (`module.disabled.*`) used by `PaneLayoutRenderer` and `NewTabPage` when the owning module is disabled.
- `module-registry.ts` now exposes `RendererResolution` metadata so renderers can stay free of UI imports (no lib→UI reverse dep).
- 478-line `register-modules.tsx` split into `register-modules/{index,editor-module,fs-backends,module-file-openers}.tsx` (transitional shim re-exports from the subdir).
- Reload-required contract locked across bootstrap: `applyModuleFileOpeners` / `registerEditorNewTabProviders` / `dispatchSettingsContributions` run at bootstrap; `PaneLayoutRenderer` snapshots `enabled` map at mount; `NewTabPage` uses `useMemo([])` to capture providers + isEnabled snapshot.
- Five rounds of codex review converged (5 → 3 → 1 → 1 → 0 findings). Two follow-ups tracked: #678 (immediate enable/disable UX redesign) and #674 (baseline test failures: TabBar pinned tooltip + sync hosts token preservation).

## [1.0.0-alpha.236] - 2026-04-28

### Feat: quick-commands v2 — Phase 1a (data layer) (#677)

Pure data layer for the v2 capability/binding/slot architecture — no UI, no executor. Phase 1b will add the Settings dialog, CommandSlot rendering, and workspace context-menu entry.

- New `QUICK_COMMAND_SLOTS` typed constants (`workspace.actions`, `host.actions`) — slot id namespace and mountTarget invariants.
- `useQuickCommandStore` adds `bindings: Record<commandId, slotId[]>` + `sanitizeBindings` (accepts any non-empty string as a forward-compat slot id per spec §2.3) + `__proto__`/`constructor` unsafe-key guard.
- Sync contributor exposes a `bindings` field; hydrate merges via `mergePersistedQuickCommandState`.
- `quick-commands` is promoted to a disableable module (mirroring editor / browser) and can be toggled in Settings → Modules.
- Two rounds of codex review: round-1 closed 2 P2 findings; round-2 ran three parallel reviewers (attacker / defender / file-quality) and closed 5 findings (prototype safety, sanitizer hardening, spec §2.3 forward-compat alignment, null-hostId ordering lock, sync normalize).

## [1.0.0-alpha.235] - 2026-04-28

### Fix(electron): stabilize mac signing for updates (#672)

Stabilize macOS app identity for packaged builds and dev updates.

- Stop explicitly disabling Electron macOS signing and verify final moved app bundles.
- Re-sign the `.app` after dev update swaps bundled `out/` resources, preserving `dev.wake.purdex` identity.
- Track `scripts/build-electron.mjs` in full-rebuild detection and add signing guard tests.

## [1.0.0-alpha.234] - 2026-04-27

### Feat(probe): probe primitive + cc + helper + dev log (#670)

Phase 4a PR-4a-1. Architectural pivot: probe layer is now **dumb** — it
observes raw screen events; the orchestrator (new
`internal/module/agent/probe_orchestrator.go`) owns status-transition
dedup via `currentStatus` comparison. No emit-once flags, no Rearm API.

- New `tmux.CapturePaneRange` + `CapturePaneTopLines` (with `-e` to
  preserve ANSI so spinner color changes still produce diff hashes).
- Probe primitive: `Watch(target, opts, cb)` long-lived watcher emits
  `ScreenChanged` on every diff tick and `ScreenStable` every
  `IdleStableTicks` consecutive identical hashes. Replaces the legacy
  `ActivityCallback` fire-once-then-exit watcher.
- New `agentpkg.ProbeProfileProvider` optional interface lets agents
  tune capture region + idle threshold. cc adopts it (TopLines: 12,
  IdleStableTicks: 3); codex/opencode fall through to default profile
  (BottomLines: 10, IdleStableTicks: 3) — same capture region as
  legacy `activityLoop`.
- Orchestrator `interpretScreenEvent` applies: stale-callback guard,
  graceWindow suppress (2s post-hook hook authority), Error Guard,
  v2.0 transition gate, atomic stale-callback revalidation, and a
  ScreenStable cheap pre-gate (skip bottom capture when already Idle
  with alive top-frame PID).
- 4 new expvar counters under `purdex_probe_*` (kept separate from
  `purdex_phase35_*`). `PDX_DEV_MODE=1` enables `[probe]` log channel.
- `manageActivityWatch` + `renameSessionLocked` migrated to orchestrator;
  `lastHookAt` migrates across rename so graceWindow stays in effect.
- Plan went through 15 rounds of codex review + a v2.0 architectural
  pivot (consulting B vetoed dedup-in-probe premise) + 3 rounds on the
  PR (1 standard + 3 adversarial + 1 convergence check). 39 new tests,
  race-clean.

## [1.0.0-alpha.233] - 2026-04-27

### Feat(agent/opencode): OpenCode 1.14.23 hooks completion (#664)

Phase 4a PR-4a-0. Audit and freeze the OpenCode 1.14.23 hook surface, switch the plugin from the deprecated `session.idle` Bus event to the canonical `session.status` filtered to `{type:"idle"}`, and ship a boundary-enforcement script for the PR-4a-0 file scope.

- Audit all 65 upstream OpenCode hooks (19 strong hooks + 48 Bus events) and freeze the catalog with a full-stage manifest + 9-event payload fixtures + provenance source map.
- Decision 3 (switch): plugin template moves from `session.idle` to `session.status` filter; single `case` swap, no Map/dedup/dual-subscribe.
- Decision 4 (defer): `session.status` busy/retry variants received-but-no-op; trigger conditions tracked in #661.
- Add HC5 / HC5b / HC5c / HC5d catalog ↔ manifest contract tests; HC5e enforces H6.4 partial-stale policy invariants.
- OC1 / OC1a contract tests (18 sub-tests) verify post-Decision-3 mapping against rendered-template payload fixtures.
- Fix wave: clear stale idle suppression on new `chat.message`; correct SupportedVersion safety-net framing in audit doc; remove legacy pluginState dead code; add rendered-template parity guard.
- Boundary script (`scripts/check-pr-4a0-boundary.sh`) uses three-dot diff to scope the file-scope assertion to HEAD changes only.
- Five rounds of codex review (R1 standard + R2a/b/c adversarial + final re-check); five fix wave commits address Round 2 findings.

## [1.0.0-alpha.232] - 2026-04-27

### Test(spa): guard OpenCode agent icon (#662)

Add focused coverage for the already-shipped OpenCode agent icon lookup.

- Verify `getAgentIcon('opencode')` renders the OpenCode SVG marker rather than a cc/Codex variant.
- Verify the OpenCode icon remains stable across the full cc/Codex icon variant matrix.
- Document PR6 as a coverage-only guard with no production icon or tab rendering changes.

## [1.0.0-alpha.231] - 2026-04-27

### Fix(agent/opencode): report hook support version (#659)

Report OpenCode hook support metadata without changing the managed plugin mapping.

- Add OpenCode `SupportedVersion=1.14.23` and `ExceedsSupport` reporting across `CheckHooks` result paths.
- Cover missing, unmanaged, drifted, installed, and version comparison paths.
- Defer deep OpenCode runtime/source contract validation to #658 to match the cc/Codex install-check verification depth.

## [1.0.0-alpha.230] - 2026-04-26

### Feat(agent): classify cc and Codex hook catalogs (#655)

Classify Claude Code and Codex hook catalogs without changing runtime install behavior.

- Add version-pinned Claude Code upstream hook catalog assertions, including non-installable `ignored` / `unsupported` events.
- Add Codex current-docs upstream subset assertions while retaining existing Purdex compatibility installable entries.
- Keep installable hook sets stable: Claude Code = 9, Codex = 9.
- Keep OpenCode event contract and mapping refresh deferred to follow-up PRs.

## [1.0.0-alpha.229] - 2026-04-26

### Chore(dev): rename `PDX_DEV_UPDATE` env var to `PDX_DEV_MODE` (#653)

Generalize the dev mode env flag to host future dev observation features (e.g., Phase 4a graceWindow logging). Pure search & replace across live code + docs; historical CHANGELOG entries and archived `docs/superpowers/{plans,specs}/2026-04-18-*` are intentionally preserved as immutable historical record.

- `internal/module/dev/module.go` — env check + log message + comment
- `internal/module/dev/{module,daemon}_test.go` — `t.Setenv` calls
- `electron/{preload,updater}.ts` — `process.env` access + comment
- `CLAUDE.md` + `AGENTS.md` — docs

`config.Dev.Update` config field is unchanged — it specifically gates the update feature; only the env var was generalized.

## [1.0.0-alpha.228] - 2026-04-26

### Fix(agent): hook install and cleanup correctness (#645)

Agent hook installer/checker hotfix. Hook handling now distinguishes classified catalog entries from installable events, and cc/Codex cleanup ownership is explicit and provider-local.

- Add `HookHandling` helpers and scope installer/checker/template completeness to installable `status/detail` events.
- Codex hook install now enables and checks `features.codex_hooks`, preserves config file permissions, and treats `PermissionRequest` as a current required event.
- Codex/Claude cleanup now removes only Purdex-owned hook commands, preserves unknown same-provider tokens and third-party shapes, and rejects unsupported hook roots/install shapes before writing.
- Codex remove no longer leaves empty event keys that Purdex later reports as broken.
- Regression tests cover installable filtering, ownership cleanup edges, unsupported shape preservation, and strict `pdx hook` command grammar.

## [1.0.0-alpha.227] - 2026-04-26

### Feat(agent): Phase 3.5b — sweep canonicalize defense-in-depth (#650)

Sweep 加第三 pass `canonicalizePane`，把 PR-3.5a hot-path 漏網的 standalone descendant frame 在 2s 內 fold 進 cross-type ancestor proxy ref，補完 DB-level eventual consistency。Defense-in-depth — user-visible correctness 已由 PR-3.5a projection dedup 保證；3.5b 把 DB 層 partial state 的存活時間從「等 SessionEnd / 1h idle / pid_dead」縮到「下次 sweep tick ≤ 2s」。13 commits（4 plan v1→v4 + 5 impl + 4 review fix）；4 plan rounds + 5 PR rounds 共 9 輪 codex review 收斂。

**Hybrid B+ 四層架構完成**

| 層 | 職責 | 落地 |
|---|---|---|
| Hot path | best-effort canonicalization；失敗不 retry/rollback | 3.5a |
| Projection | 隱藏 partial（**唯一 strongly consistent 邊界**） | 3.5a |
| **Sweep** | **bounded-time recovery（2s）** | 3.5a prune + **3.5b canonicalize** |
| Observability | partial 發生率 expvar | 3.5a + 3.5b 接 `MetricSweepCanonicalized` |

**新增 / 改動**

- `internal/module/agent/frame_ops.go`：抽出 `candidateHasOwnedState(candidate)` 共享 helper（mirror hot-path inline block 1098-1137；pure refactor 0 行為變更）
- `internal/module/agent/sweep.go`：
  - `canonicalizePane(paneID, broadcastTs)` — 四 case branching：silent-skip（parent 已有 ref + owned state）/ delete-only（parent 已有 ref + 無 owned state）/ attach-only F2 recovery（parent 缺 ref + owned state）/ attach+delete 主路徑
  - `findCanonicalAncestor(candidate, framesByPID)` — PPID 鏈走 `proxyMaxDepth=5` 找 cross-type live identity-verified ancestor frame
  - `broadcastProxyCanonicalized` — per-pane broadcast pattern
  - `sweepOnce` 第三 pass：每 pane `canonicalizePane` 跑在 `pruneDeadProxyRefs` 之前
  - **F1+F4 hoisted ancestor revalidation** — `isPidAliveFn` + `processStartTimeFn` 在 DeleteIfUnchanged 前共同前置點重驗，覆蓋 attach-then-delete 與 delete-only 兩條路徑
  - **F2 owned-state attach-only recovery** — 在 parent 缺 proxy ref 且 child 有 owned state 時 attach（projection_dedup 可隱藏 + merge），不刪 row 保留 native state
- `internal/agent/metrics.go`：`MetricSweepCanonicalized` 接上 caller（PR-3.5a 已宣告但無 caller）

**測試**

- 19 個 integration tests（IT10 + IT10b-IT10s）涵蓋 race interleavings、identity gates、partial recovery 路徑、F1/F2/F4 closure regression guards
- 11 個 unit tests（RC6-RC11 `findCanonicalAncestor` + RC12-RC16 `candidateHasOwnedState`）
- 23 packages 全綠；無 skip

**Codex review trail**

- Plan：R1 high (hasOwnedState 缺) → v2 / R2 high (IT10n 不精準) → v3 / R3 approve
- PR：R1 standard clean / R2 三角度 3 findings (F1 attacker high + F2 defender high + F3 file-health medium) / R3 closure F4 high (delete-only bypass) / R4 closure 1 medium (plan drift) / R5 approve
- Fix commits：F1 → `3c4ebd07` / F2 → `38eef1a6` / F3 → `a2aafdf2` / F4 → `f46f616c` / Plan v4 → `d8c454cf`
- 多輪 high 嚴重性遞減符合 `feedback_codex_review_termination.md` 終止條件

**Plan**

`docs/specs/2026-04-26-lights-rebuild-phase-3-5b-plan.md` v4（含完整 review trail）

## [1.0.0-alpha.226] - 2026-04-26

### Feat(agent): Phase 3 — daemon restart frame recovery + no_parent_fallback (#638)

Lights rebuild Phase 3。daemon 重啟後若 hook 事件抵達時 frame 不在記憶體，改走 PPID descendant tree 重建擁有者；沒有 parent 時走 explicit `no_parent_fallback` reason 而非舊的 binary `parent_frame_found / missing`。2 輪 plan review + 2 輪 code review 收斂。

- `Prober.FirstAliveAgentInTree` + `identifierOrder` slice 提供 PPID descendant tree 上對 cc/codex/opencode 的 deterministic first-match（避開 Go map iteration 不穩定）。
- `tryRebuildFromProcessTree` helper 在 `applyFrameEvent` fallback 鏈中先試重建 frame ownership，再退到 `no_parent_fallback`。
- 三態 reason（`parent_frame_found` / `daemon_restart_recovery` / `no_parent_fallback`）取代舊的 binary，配合 grep guard 確認 production code 無遺留舊字串。
- `FrameTraceMeta.MatchedAgentType` 把 type 信號保留到 trace step `after` payload（給 Inspector / 未來 reparent 使用）。
- Code R1（standard）：`PanePID` 在多 pane window 會選到錯 pane → 改用 `ActivePanePID`，補測試與註解。
- Code R2（adversarial）：fail-soft missed identifier 在 `FirstAliveAgentInTree` 會 panic → double-deferred recover 包 `safeIdentifyPID` + outer top-level，新增 P7/P8 + R9/R10 regression。
- Tests：23 個 probe / frame_ops / handler 測試（P1-P8 / R1-R3+R5-R8+R9-R10 / N1-N3 / I1-I3）；既有 PR-2b 0 regression。Pre-existing same-type bugs（`verify.go:82` / `IsAliveFor`）out of PR scope，由 issue #639 追蹤。

### Feat(agent): Phase 3.5a — cold-start proxy canonicalization (#644)

修正 PR-2b（alpha.221）`findProxyParent` 留下的 cold-start race：兩個跨 type 的 SessionStart 同時抵達同一 pane（典型情境：daemon 重啟同時 cc + codex proxy spawn）時，pre-Upsert PPID walk 兩邊都 miss，造成兩個 standalone frame，少了 canonical parent + proxy ref。Hybrid B+ 設計（consulting 驅動）— 接受 partial state 為 ephemeral telemetry table 的合法狀態，user-visible correctness 由 daemon-side projection dedup 保證；DB-level eventual consistency 留給 sweep（PR-3.5b 後續）。5 輪 codex review，14 個 finding fix + 2 個 deferred。

**後端 race fix（`internal/module/agent/frame_ops.go`）**

- `pidIsAncestorOfWithCap`（深度 5）走 descendant 的 PPID 鏈找 ancestor PID。
- `canonicalizeDescendantsAfterUpsert`（self-as-ancestor）：ancestor SessionStart 後掃 pane 上跨 type、live、identity-verified 的 standalone descendant，PPID 鏈穿過 self 就 fold 成 proxy ref + best-effort 刪除原 standalone 列。
- `reconcileCreatedFrameAsProxy`（self-as-descendant）：descendant 在 post-Upsert 走 ancestor walk + attach proxy ref + best-effort delete 自己 standalone frame；**沒有 rollback** — partial state 合法、由 sweep 修。
- `applyFrameEvent` 三段 wiring：§2.2.1 new-frame post-Upsert（reconcile → descendant-scan）、§2.2.2 existing-frame **filter-merge-retry**（取代 v5 三步 snapshot+reset+re-attach 的 concurrent-attach race）、§2.3 SessionEnd hot-path proxy cleanup（best-effort `removeProxyRefForSender`，先 detach 後 delete）。
- Cross-frame native ID collision（S1）：proxy dedup baseline 改 owner-aware，`(IsProxy, ID, SourcePID, SourceStartTime)` 同時比對。
- Filter-merge baseline（T1）：`(Type, ID, StartedAt)` 三元組做 identity gate，避免 ID-only 漏 reset 同 ID 的舊 subagent。

**Projection dedup（`internal/module/agent/projection.go`）— 唯一 strongly-consistent 邊界**

- `buildPaneProjection` 在 parent.Subagents 含 `IsProxy + (SourcePID, SourceStartTime)` 時，把對應的 standalone frame 從 `TopFrame` 候選中排除 — SPA 永遠看不到 partial。
- 全部被 claim 時 fallback 回 unfiltered（極端 edge case）。

**Observability（`internal/agent/metrics.go`，新檔）**

- 4 個 expvar counter（in-process）：`partial_state_observed` / `projection_dedup_applied` / `partial_canonicalization_created` / `sessionend_cleanup_applied`。Endpoint exposure 之後 follow-up。

**Tests — 全部 23 packages 綠**

- Unit：RC1-RC8 race interleavings、PD1-PD3 projection dedup edge cases。
- Integration（real sqlite）：IT1-IT9 + IT11 + IT12 + IT15-IT20，含 IT4 ancestor-late race（核心 J1/F1 regression guard）、IT12 SessionEnd ghost dot（Side B 發現的 bug）、IT20 concurrent attach during retry（J1 regression guard）。

**Plan + review trail**

- Plan v1→v12，2 輪平行 architectural consulting（Side A SQL transaction vs Side B Hybrid B+）。v3→v4 為 paradigm shift（接受 partial state 取代 patch-over-patch retry/rollback）。
- 5 輪 codex review 後 finding 收斂在 1 high/輪（diminishing returns）；所有 high/medium 修完，2 個 deferred 開 follow-up issue（N1 projection dedup hot-path liveness syscall perf；IT12b detach error injection 需要 `m.frames` interface 抽象 refactor）。
- 完整 plan：`docs/specs/2026-04-25-lights-rebuild-phase-3-5-plan.md`。

## [1.0.0-alpha.225] - 2026-04-26

### Feat(spa): add tab name tooltip controls (#642)

Tab name tooltip 新增可設定顯示範圍，並延後到滑鼠懸停 800ms 後才顯示，降低短暫滑過 tab 時的視覺干擾。

- Terminal 設定在 Dynamic tab name 下方新增 `Tab name tooltip`，可選不顯示、上方、左邊或都顯示。
- 上方 tab 與左側 activity bar inline tab 依設定分別控制完整 tab name tooltip 是否渲染。
- `HoverTooltip` 新增 800ms 延遲與離開前取消邏輯，並補對應 regression tests。

## [1.0.0-alpha.224] - 2026-04-25

### Fix(spa): keep activity bar home fixed (#640)

Activity bar 的 Home、workspace 分隔線與底部 actions 固定在原位，只有 workspace 清單區域捲動；同時修正 tab hover tooltip 被 overflow 容器裁切的問題。

- 窄版與寬版 activity bar 都改為 Home / divider / workspace scroll / footer actions 的固定結構，避免 Home 被納入捲動範圍。
- workspace 分隔線固定在 scroll 區外並加上 `shrink-0`，防止 flex 壓縮導致分隔線忽隱忽現。
- workspace scroll bar 使用 Purdex theme token 與透明 track，符合現有視覺風格。
- `HoverTooltip` 改用 body portal + fixed positioning，讓上方 TabBar 與左側 inline tabs 的完整 tab name tooltip 不再被 overflow 裁切。
- 補 activity bar scroll boundary、divider placement 與 tooltip portal regression tests。

## [1.0.0-alpha.223] - 2026-04-25

### Fix(spa): restore tab bar shrink behavior (#636)

Tab bar layout 修正，讓 tabs 在空間不足時先縮到最小寬度，再進入水平捲動，並清掉 overflow/active 視覺瑕疵。

- normal tab strip 改回可吃剩餘寬度並加上 `min-w-0`，移除 `min-content` minimum 導致 tab 無法 shrink 的約束；保留 `max-content` 讓 tab 少時新增按鈕仍貼在最後一個 tab 後方。
- 左右 scroll controls 拆成「漸層 fade 區 + 不透明按鈕區」，避免箭頭底下透出 tab label。
- active tab 改用透明 border，移除上緣不等長亮色邊框，同時保留 border 佔位避免尺寸跳動。
- 補 `TabBar` / `SortableTab` regression tests，鎖住 shrink layout、右側 scroll fade 結構與 active tab border 視覺。

## [1.0.0-alpha.222] - 2026-04-25

### Feat(agent): add tmux metadata dynamic titles (#634)

Agent dynamic title 改以 tmux active pane metadata 作為 source of truth，並在 Host > Agents 補上 `allow-set-title` integration 狀態與安裝/移除控制。

- 後端新增 tmux active pane metadata 查詢與 sanitize，讓 session/tab display 可從 pane title、window name 與 pane command 推導 agent title。
- Host > Agents 新增 `Agent title` 區塊，可檢查/安裝/移除 Purdex managed `allow-set-title` marker block；remove 不強制關閉 tmux runtime option。
- `allow-set-title` runtime check 改用 `tmux show-options -w -g -q -v`，避免新版 tmux 的 `show-window-option -q` 不支援錯誤。
- SPA title preference、agent store 與 sync contributor 同步支援新的 dynamic title source。

## [1.0.0-alpha.221] - 2026-04-25

### Feat(agent): Phase 2 PR-2b — proxy detection + idle sweep + SubagentDots visual (#631)

Lights rebuild Phase 2 收尾棒（PR-2a schema + wire 已於 alpha.218 落地）。把 PPID 祖先鏈 proxy 偵測、frame idle sweep、SubagentDots 視覺升級、以及 `subagents_json` 的多路 atomic RMW 全在一個 PR 內收齊。13 commits / 5 feat + 7 review-fix + 1 R4 test stub / 9 輪 codex review 全收斂 / R9 RMW inventory ship-ready approve。

**後端（internal/agent + internal/store + internal/module/agent）**

- **PPID 祖先鏈 proxy 偵測**（`findProxyParent`，depth=5）：cc pane 跑 `/codex:*` 不再建獨立 codex frame，而是掛進 cc parent 的 `Subagents` 標 `IsProxy=true`；live-only same-type hard-stop（dead/PID 重用的 stale 同 type ancestor 走 skip+walk-up，不阻 walk）+ `(SourcePID, SourceStartTime)` start_time identity 三分支
- **SessionEnd proxy cleanup**（`removeProxyRefForSender`）：proxy 來源 agent 結束時從 parent 移除 ref
- **Idle sweep 第三條規則**（1h + conditional DELETE）：無 hook 活動 1h 的 frame 被 sweep 清；用新 `DeleteIfUnchanged` store method 防 concurrent refresh 被 clobber；`setProjectionTopStatus` 在 probe 活動轉 Status 同時 bump `LastSeenAt` 避免誤清 live frame
- **`clearFrame` 拆 `afterFrameCleared` 共用 helper**：pid_dead / pid_reused / idle_timeout 三路徑統一 side-effects pipeline；同步修正 orphan StopWatch bug（清 frame 時若 session 有 active activity watcher 補 `StopWatch`）
- **Kind-aware `subagentRefMatches`**：`updateSubagents` 比對時 proxy 用 `(SourcePID, SourceStartTime)`、native 用 `ID`，`IsProxy` 不同永不 match — 防 proxy 合成 ID 字串與 native agent_id 撞
- **Atomic RMW on `subagents_json`**：新 `UpsertIfUnchanged` + `mutateSubagentsWithRetry` helper（proxy attach/detach + native SubagentStart/Stop 共用，bounded retry `proxyUpsertMaxAttempts = 3`）；probe path / general hook path / SessionStart reset 改 narrow column updates（`UpdateStatusAndLastSeen` / `UpdateHookPath` / `UpdateHookPathAndResetSubagents`），不碰 `subagents_json` — race 消失於 SQL 層

**SPA（spa/src/components + hooks）**

- `SubagentDots` API 從 `{count}` 升 `{refs: SubagentRef[]}`：每 dot 依 agent type 上色（`TYPE_COLOR` cc 藍 / codex 黃 / opencode 橘），proxy ref 渲染 hollow ring outline，native ref 保 solid fill
- 5 consumer props 同步：`TabIcon` / `SortableTab` / `InlineTab` / `renderInlineTabIcon` / `useTabDisplay`

**Tests**

- 29 新 Go tests：PR1-PR17（proxy walk + same-type hard-stop + identity 三分支 + R3/R6 race regression）/ F4-F9（store DeleteIfUnchanged / UpsertIfUnchanged / narrow updates）/ SE1-SE3（SessionEnd proxy cleanup）/ IS1-IS7（idle sweep + R1/R7 LastSeenAt bumps + RMW probe path）/ HB2（broadcast shape）/ U6-U7（kind-aware match + retry helper）/ HookRace1（R8 general hook RMW guard）
- 12 新 SPA tests + 1 regression（SubagentDots TYPE_COLOR + outline + 5 consumer props 整合）
- Go build/vet/test clean / SPA lint+build+vitest clean（baseline 3 hosts.test.ts pre-existing 無關）

**Review 收斂（9 輪）**

R1 standard（1 P2 idle sweep live-frame regression → `f26888a9`）/ R2 3-parallel adversarial（1 P1 proxy/native ID collision → `9f29e123`）/ R3 sanity（1 P1 stale same-type 阻 walk → `c46de72b`）/ R4 sanity（test coverage stub → `16485c44`）/ R5 final-gate（1 P2 proxy attach race，使用者判斷 in-PR 修 → `9ca25a3f`）/ R6 sanity（1 P2 native SubagentStart/Stop race → `269022e4`）/ R7 sanity（1 P2 probe status RMW race → `bd729cb7`）/ R8 sanity（1 P2 general hook RMW race → `bc39cbb3`）/ R9 RMW inventory（0 findings — approve ship-ready）。所有 9 路 `subagents_json` 寫路徑 R9 入清單分類驗證：proxy attach/detach + native SubagentStart/Stop 走 atomic-retry / 一般 hook 走 narrow / SessionStart reset 走 narrow-reset / new frame 走 insert-only / SessionEnd / probe / replay / migrate 各自封口。

關閉 #632（Proxy attach/detach on Subagents is not atomic — R5 follow-up，已在 R5/R6 兩路 atomic helper 一併修完）。

**關鍵技術決策**

- Proxy ID 格式：`fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime)` — kind-aware identity key 防 native agent_id 撞
- Proxy walk depth = 5（cover codex→codex-companion→cc layout + 3 hops buffer）
- Same-type hard-stop 前置於 alive + identity gate
- Store API invariant：`Upsert` 只給 new-frame insert；`UpsertIfUnchanged` 服務所有 RMW；narrow updates 不碰 `subagents_json` 除 explicit reset

## [1.0.0-alpha.220] - 2026-04-25

### Fix(spa): Editor UI polish — rotation, alignment, line highlight, settings layout (#629)

Editor Restructure 落地後的四個小 polish，純視覺對齊、無邏輯變動。

- 拼圖片（module-owned contribution marker）改為往右 30° 旋轉（原本 -12°），在 sidebar 10-12px 下輪廓更清晰；SettingsSidebar / HostSidebar / WorkspaceSettingsPage 三處同步
- EditorPurdexSettingsSection 重寫對齊 canonical pattern（Appearance / Terminal / Sync / Interface 共用的 `<h2>` + `<SettingItem>` 佈局），並把所有字串抽到 `settings.editor.*` i18n keys（EN + zh-TW 共 16 條）
- EditorToolbar 麵包屑路徑原本 `items-start` + `pt-0.5` nudge 導致文字偏上；改為 `items-center` 讓 path 與 action 按鈕同軸
- Monaco 當前行高亮從預設邊框改為 VSCode 風格的淡背景亮度：新定義 `purdex-dark` theme 繼承 `vs-dark`，將 `editor.lineHighlightBorder` 設為透明、`editor.lineHighlightBackground` 設為 `#FFFFFF0D`（約 5% 白）

## [1.0.0-alpha.219] - 2026-04-24

### Feat(spa): Editor restructure — Buffers pane + HSR migration + breadcrumb popover (#623)

把 Editor 模組收攏到 PR #617 的 module-owned contribution framework 下，同時把 `/buffer/*` 從 legacy settings tab 升級成正式 pane + breadcrumb popover 快切，並透過 `EditorPurdexSettingsSection` 讓 Monaco 六個 preference 成為使用者可控。六個 commit / 53 tests / 六輪 codex review（3 spec + 3 PR-diff）。

- 新 `useEditorSettingsStore`（Zustand + persist，key `purdex-editor-settings`）：`tabSize` / `insertSpaces` / `wordWrap` / `lineNumbers` / `minimap` / `fontSize` 六欄位 + 合理 defaults + clamp / merge-sanitize rehydrate；S1-9 regression guard 鎖住 happy-path rehydrate
- `MonacoWrapper.tsx` 所有 option 從 store 讀，無 hardcoded 殘留
- Editor 模組取得 `localId:'editor'` HSR entry（scope:`purdex`, order:9）；legacy `registerSettingsSection({id:'editor-buffers',...})` 移除；sidebar "Editor" 行為**首個**帶 puzzle-piece marker（`isModuleOwnedContribution` 首次 true）
- `SettingsPage` 對舊書籤 `/settings/editor-buffers` URL 做 alias redirect 自動導到新 section；i18n `settings.section.editor_buffers` → `settings.section.editor`（EN "Editor" / zh-TW "編輯器"）
- 新 pane kind `editor-buffers`：list（`/buffer/*` 直子，name-asc 排序）+ toolbar（New / Rename / Delete / Open）+ multi-select + smart-open + empty-state；`ManageBuffersNewTabCard`（order 6, `moduleId:'editor'`）以標準 `onSelect` 取代當前 NewTab pane（**非** singleton）
- Smart-open（spec §4.6）：active tab → tabOrder scan → fallback 新 tab；eligibility filter（`source==='inapp'` AND buffer 非 dirty）— 來源不同或 dirty 的 pane 跳過，永不靜默吞資料
- Delete flow（spec §4.9.5）：locked-tab 硬拒 + dirty-specific confirm + single-confirm；close loop 同時呼叫 `useTabStore.closePane` **與** `useEditorStore.closePane(paneId, bufferKey)` 避免背景 tab 的 stale buffer 復活已刪檔（G2）
- Rename：pre-check `backend.stat` 防止覆寫同名（F4）；`performBufferRename` helper 三步 sync（`backend.rename` → `useTabStore.renameEditorPanes` → `useEditorStore.renameBuffer`），rename 後 pane `filePath` + buffer key + paneState.bufferKey + buffer metadata（language / languageSource）全部跟進（G1 + R6 MED）
- Breadcrumb popover：EditorToolbar 的 Purdex chip 在 `source.type==='inapp'` 時成為 `<button>`；React portal + z-100（高於 Monaco popup）；列出所有 `/buffer/*`（aria-current 標當前）+ "Manage buffers..." 連結；`onSwitch` / `onNewBuffer` 帶 dirty guard（`window.confirm`）
- `openBufferByName(name)` helper — toolbar Open 與 row double-click 走同一入口，用顯式 name argument 取代 stale `singleSelected` closure（G3）
- Ancillary switch sites：`tabToUrl` / `getPaneLabel` / `getPaneIcon` 全加 `editor-buffers` branch；`NewTabProvider.moduleId?:string` 新欄位 + `NewTabPage` module-aware filter；NewTabPage 全 column filter 為 null 時 fallback 到 empty state（F8）
- Refactor：`createMetadata` / `detectLanguage` / `detectLanguageSource` / `untitledStoragePath` / `untitledSuggestedName` 抽到新的 `spa/src/lib/editor-language.ts` util，兩個 editor 元件共用，防止下一次 rename 流程再度漂移

53 個 Vitest cases：S1-1..9 / R1-1..5 / L1-1 / M1-1 / P2-1 / B2-1..18 / B2-16b / B2-16c / N2-1..3 / A2-1..7 / C3-1..7 / T3-1..2。

Review 歷程：Spec 三輪（R1 15 findings → R2 3 HIGH → R3 4 HIGH，全吸收 v1.1→v1.3）→ PR-diff 三輪（R4 1 CRIT 撤回 + 7 HIGH 作 Commit 4；R5 0 CRIT + 2 HIGH + 4 MED + 1 LOW，must-fix G1/G2/G3/G7 作 Commit 5；R6 focused adversarial 9/10 probe clean + 1 MED 作 Commit 6）。R5 的 G4/G5/G6 開 follow-up issue #625 / #626 / #627。



## [1.0.0-alpha.218] - 2026-04-24

### Feat(agent): Phase 2 PR-2a — SubagentRef schema + wire breaking upgrade (#622)

Lights rebuild 系列 Phase 2 第一棒，把 subagent 從 ID-only 字串升級為結構化 `SubagentRef`（agent ID + canonical Type + StartedAt + SourcePID + SourceStartTime + IsProxy），為下一棒 PR-2b 的 proxy 偵測 / frame idle sweep / SPA 視覺區分鋪路。此棒只升 schema + wire + migration，視覺仍為 count-based。

- 新型別 `internal/agent/subagent.go` — `SubagentRef{ID, Type, StartedAt, SourcePID, SourceStartTime, IsProxy}`；`IsProxy,omitempty`；`source_pid` / `source_start_time` 非 omitempty（native refs 顯式序列化 0）；`Type` 固定用 `frame.AgentType`（canonical agent family，不吃 detail.agent_type）
- `store.Frame.Subagents` / `SessionProjection.Subagents` / `Module.subagents` map / `NormalizedEvent.Subagents` / SPA `useAgentStore` — 全部從 `[]string` → `[]SubagentRef`
- `updateSubagents(current, eventName, ref) → refs` 取代 string 版
- **Schema upgrade safety**：`migrateFramesDB` 掃全表 `subagents_json` 三態處理 — new schema ✅ pass / legacy `[]string` → truncate / malformed → `fmt.Errorf` block daemon 啟動（operator-visible，不靜默吞）
- `Module.New` frames init 失敗 fatal（不吞）；traces init 失敗 log warning + continue nil（best-effort，不阻啟動）
- SPA TabIcon / SortableTab / InlineTab / renderInlineTabIcon 仍 count-based — 視覺升級留給 PR-2b
- 新 test helper `store.AgentEventStore.ExecRawForTest` + `framesInitFn` / `tracesInitFn` test seams（顯式 ForTest 命名 + cross-package test seed）
- 14 個新 Go tests：SubagentRef JSON R1-R3 / store F1-F3 / updateSubagents U1-U5 / HB1 broadcast shape / Migration legacy-mixed-malformed-new（4）/ New fail-on-malformed + traces-best-effort（2）；配 ~20 SPA fixture upgrade
- **破壞式升級通知**：daemon 首次啟動會自動把舊 `[]string` 形式的 `subagents_json` truncate 為空陣列；如遇 malformed JSON 會 refuse 啟動並 log row id，需手動修（不會靜默丟資料）

11 commits：5 subagent TDD + 6 codex fix + 6 plan docs。Plan review 5 輪（v1→v6，findings 4→2→2→2→0）；Code review 4 輪全採納：R1 標準（P1 legacy DB 500 + P2 canonical Type）、R2 3 parallel 攻防體質（P1 LIMIT 1 probe miss mixed + P2 malformed wipe）、R3 sanity（P1 Module.New 吞 err）、R4 sanity（P1 R3 修過頭 traces 誤 fatal）。



### Feat(agent): Lights rebuild — Hook events declaration (#613) + §2.4 guardrails (#616)

把 Hook events 的 SSoT 從分散在三家的 emitter 集中到 `HookInstaller.Events()`，讓 installer / CheckHooks / SupportedStatuses derivation / 未來 Inspector UI 讀同一份 `HookEventSpec` 清單；順帶補齊 codex 9-event installer、drift 測試、plan spec §2.4 架構護欄，以及上一輪 code review 後三輪 codex review 的 11 個 findings 修復（含 FutureOnly bit、byte-exact template 比對、quote-aware command tokenizer 等）。

- `HookEventSpec{Name, EmitsStatus, Description, FutureOnly}` 取代分散在三家的 string slice；`DeriveSupportedStatuses(specs)` 以 union 推導 `SupportedStatuses()`
- `HookStatus{Managed, UpgradesAvailable}` + `HookEventInfo{FutureOnly}` 新欄位讓 UI 區分三面向：是否受管（Remove 按鈕條件）、可否升級（Install 按鈕條件）、per-event 是否為 FutureOnly tolerated-absent
- codex installer 擴張到 9 event（3 主線 + 6 FutureOnly: SubagentStart/Stop、StopFailure、Notification、PermissionRequest、SessionEnd）；legacy 3-event 安裝保持健康且可自助升級
- codex `CheckHooks` 嚴格 per-event 驗證 — `isPdxCommandCodexForEvent(cmd, eventName)` + quote-aware `tokenizeCodexCommand` 支援 `/Applications/Purdex Beta/pdx` 含空白路徑
- opencode `CheckHooks` 改 byte-exact template 比對；`resolveCanonicalPdxPath()` 用 `os.Executable() + EvalSymlinks` 取 trusted path，檔內 `pdxPath` 不再自我圓滿
- drift test per-event 強相等斷言；template/specs parity 改 `TestTemplateSpecsParity` test-only guard（`renderManagedPlugin` 不再 runtime panic）
- `HookModuleCard`：Remove 按鈕 `managed ?? installed`（tmux 等無 managed 欄位 API 相容）；Install 對 `upgradesAvailable` 非空仍 enable + FutureOnly event 灰色標籤
- codex review 軌跡：4 輪 plan review（findings 4→3→2；v4 user-cancel）+ 2 輪 4 路 code review（第 2 輪 4 findings + 第 3 輪 5 findings）+ 1 輪限定 standard review（2 findings）— 全數採納
- deferred finding #5 追蹤為 follow-up（結構化 parser / codegen 取代 regex；install/check 路徑也跑 validator）

## [1.0.0-alpha.216] - 2026-04-24

### Feat(spa): Modules Switchboard + module-owned contribution marker (#617)

把閒置已久的 Settings → Modules tab 重新實作為 **Module 開關面板**（switchboard），並加上一個 puzzle-piece UI marker，讓使用者可以辨識哪些設定入口來自真正的 module（非 built-in / legacy adapter）。

- `ModuleDefinition.disableable?: boolean` + `descriptionKey?: string` — 預設 `false`，opt-in 形式，core module 不進 switchboard
- `useModuleEnabledStore` — persist 的 `Record<moduleId, boolean>` + session 內 in-memory baseline；`hasPendingChanges()` 驅動「Reload required」banner
- `buildSettingsContributionBatch` 對 disabled module 整批跳過 `settings: [...]` dispatch（purdex / workspace / host 三 scope 皆影響）；legacy adapter 與 host-builtin contributions 不受影響
- 初期 3 個 module flag `disableable: true`：`editor` / `browser` / `memory-monitor`
- `ModulesSwitchboardSection` 註冊在舊 `module-config` 位置（URL 保留），每行：名稱 + 描述 + Toggle + 上方 Reload banner
- `isModuleOwnedContribution(c)` helper（`moduleId` 不以 `_builtin.` 開頭）→ `SettingsSidebar` / `HostSidebar` / `WorkspaceSettingsPage` 在 row/section title 尾端渲染 `PuzzlePiece`（rotated -12°）
- 關閉 #574；兩輪 codex review 4/4 採納（SR-2 + AR-1/2/3），1 個 deferred finding 建 issue #618（legacy `globalConfig` / `workspaceConfig` + `ModuleConfigSection` 全面退場 cleanup）

不在本 PR（追蹤中）：
- PR 2：Editor Buffers → Editor 重命名 + HSR migration + Buffers 管理 pane + breadcrumb quick-switch popover
- PR 3：pane-level / fs-backend / file-opener disable 整合 + runtime hot-switch + workspace-scope legacy filter（之後 `files` 可重新 flag `disableable`）

## [1.0.0-alpha.215] - 2026-04-23

### Feat(agent): Lights rebuild — Phase 1 L2 status alignment (#612)

Lights 子系統 rebuild 系列第二棒，把 Phase 0 留白的宣告矩陣填滿、補齊 codex 事件覆蓋、處理未知/已知不可導出事件的觀測性。

- 三家 provider（`cc` / `codex` / `opencode`）實作 `SupportedStatuses()`，宣告 `[Running, Waiting, Idle, Error, Clear]`
- `codex/status.go` `DeriveStatus` 補齊 5 個事件：`Notification`（permission/elicitation→Waiting，idle/auth_success→Idle）、`PermissionRequest`、`SubagentStart`/`SubagentStop`（detail-only）、`SessionEnd`（Clear）、`StopFailure`（Error）
- `spa/src/lib/agent-icons.tsx` 補上 OpenCode brand icon（`@lobehub/icons-static-svg/icons/opencode.svg`）
- `handler.go` 新加 invalid-result 早退分支：`DeriveResult.Reason` 區分 `event_not_in_catalog` / `compact_ignored` / `notification_unknown_type`，trace 寫一筆 verify-kind step + 清除 legacy `agent_events` row（避免 replay/snapshot 回灌）
- Error guard 對 `SessionEnd` 統一放行（codex StopFailure→Error 後 SessionEnd 終於可清）
- 新檔 `internal/agent/drift_test.go`：宣告 ↔ 實作的 per-fixture + set-equality 雙重斷言，確保刪掉任一 Notification 子分支會被抓到

13 commits（8 implementation + 5 review-fix）。兩輪 codex review（標準 + 3 parallel 攻防體質），6 findings 採納修復（5 必修 + 1 doc + issue），2 follow-up issues：#613（codex hook installer alignment）+ #614（file-quality cleanup）。

## [1.0.0-alpha.214] - 2026-04-23

### Feat(agent): Lights rebuild — Phase 0 Status alignment skeleton (#610)

Lights 子系統 rebuild 系列第一棒，導入最小骨架讓後續 Phase 可對齊 agent Status。

- 新 optional interface `StatusSupporter`（`SupportedStatuses() []Status`）— 與既有 `HookInstaller` / `StatuslineInstaller` 同型
- 新 `Coverage(Registry)` helper 產出 agent 宣告矩陣（`[]CoverageRow`）— 保留 registry 註冊順序（Claim priority 語意）、`Declared` defensive copy、nil → `[]Status{}` normalize
- 零改動既有 provider / handler / module / probe — Phase 0 僅骨架，Phase 1 才各 agent 填入實作
- 附帶完整 tightened 版 Lights rebuild spec（6 Phase roadmap）+ Phase 0 TDD plan + 討論文件

6 commits + 2 codex review（R1 標準 approve / R2 三視角 3 findings 採納 + 2 findings 維持判斷 + 1 findings 事實承認修 docs）。8 個 Coverage 測試涵蓋空 registry / 已宣告 / 未宣告 / defensive copy / nil normalize / 註冊順序 / duplicate AgentType / 混合情境。

## [1.0.0-alpha.213] - 2026-04-23

### Feat(spa): HSR PR-5 — Editor homePath first module user + #540 immutable snapshot + deprecation warn (#604)

HSR 系列最後一棒。Editor module 透過新 `ModuleDefinition.settings` 宣告 `workspace-home-path` + `host-home-path` 兩個 contribution，作為整套 registry + 三層 store 架構的 real module validation proof。

- Editor tilde path 層疊 resolve：workspace settings → host settings → pane shell fallback
- `LinkContext` 擴 `workspaceId?`，由 `SessionPaneContent` 用 `findWorkspaceByTab()` 查後 plumbing 到 `TerminalView`（非 active — multi-workspace inactive pane 正確性）
- 三層 store `get()` 改回 frozen deep-clone，WeakMap 處理 alias / cycle（closes #540）
- 新增 `removeKey(scope, moduleId, key)` store API — section 清 homePath 不會誤刪 sibling 鍵
- file-path opener 在 await 前 snapshot link-source workspaceId；standalone pane 延後讀 active
- Editor home path input：focus ref + dirty ref + live store re-read，防 BroadcastChannel 同步覆寫 / stale snapshot
- `ModuleDefinition.globalConfig` / `workspaceConfig` 加 `@deprecated` + register-time warn（豁免 `files`；de-dupe；HMR reset 會清）

17 commits：plan §8 6 + R1 codex 3 + R2 codex (3-way adversarial) 5 + R3 codex 3。4 輪 review 收斂 HIGH/P1 → P2/P3 → known issues 追蹤（#607 workspace layer 在 mixed-host workspace 的 host scope 缺口 / #608 workspace 刪除中 opener orphan tab race）。

測試新增 ~75 個 PR-5 cases 全綠，完整 suite 2644 passed（3 pre-existing hosts.test.ts fail 無關）。

## [1.0.0-alpha.212] - 2026-04-23

### Fix(dev): exec rebuilt daemon binary after rebuild (#605)

- `POST /api/dev/daemon/rebuild` 在成功 rename `bin/pdx.new -> bin/pdx` 後，原本仍用目前行程的 `os.Executable()` 做 self-exec。當 daemon 不是從 repo 內的 `bin/pdx` 啟動時，重啟會回到舊 binary，UI 會看到 build 完成與 restarting，但實際 daemon 版本不會更新。
- 現在改為直接 exec 剛完成原子替換的 `bin/pdx`，確保 daemon self-rebuild 真的切到新 binary。
- 新增回歸測試，驗證 rebuild SSE 跑完後，exec target 指向 repo 內重建出的 `bin/pdx`。

## [1.0.0-alpha.211] - 2026-04-23

### Fix(agent): preserve symlink invocation path for versioned CLI binaries (#602)

- Claude Code 2.1.113+ 以 symlink (`~/.local/bin/claude`) 指向版本化 Mach-O binary (`~/.local/share/claude/versions/<X.Y.Z>`)。原先 `normalizeExecutablePath` 的 `EvalSymlinks` 會把 `ExePath` 解析成版本化 target，`cc.Provider.Identify` 看到 basename `"2.1.117"` 而非 `"claude"`，導致每個 cc hook 都以 `identify_mismatch` 在 verify 階段被拒絕。
- Spoofing 防線仍由 `internal/module/agent/verify.go:63` 的 `pidAncestorIncludesFn` 提供（要求 sender PID 在目標 pane 的 process tree 內），比 binary path 匹配更強。
- 測試：新增 unit test `TestNormalizeExecutablePath_PreservesSymlinkInvocationPath`；既有整合測試 `TestProcessInfo_ResolvesSymlinks` 改名為 `TestProcessInfo_PreservesSymlinkInvocationPath` 並翻轉 assertion。

## [1.0.0-alpha.210] - 2026-04-23

### Fix(spa): focus WYSIWYG editor from empty clicks (#600)

- `TiptapEditor` 把 Live Mode 空白區點擊視為 editor focus 請求，現在點擊整個 WYSIWYG 畫布都會把焦點導回實際的 `contenteditable` 輸入區。
- 直接點在現有 editable 內容上的行為維持不變，只補上原本落在空白區時不會 focus 的缺口。
- 新增回歸測試，覆蓋點擊 editor 空白區會觸發 focus 的行為。

## [1.0.0-alpha.209] - 2026-04-23

### Feat(spa): HSR PR-4 follow-up — #586 batch-replace + #588 reactive host runtime (#593)

- **#586 host built-in batch-replace + transactional commit**：`registerBuiltinHostSection()` + pending-buffer/drain 移除，改為 `setHostBuiltinSections(defs[])`（一次性 stage）+ `commitHostBuiltinSources()`（dispatch Phase 2 內 staged → live 原子 swap）。Wrapper identity 按 localId stable cache（HMR / drop+re-add 都不換 React component reference）；wrapper render 時讀 `liveSources` — dispatch 失敗時 `liveSources` 不變，不會出現 sidebar/route 與 body 內容 split-brain。`dispatchSettingsContributions()` 對 host built-ins 完全 idempotent（第二次 standalone call 不再清空，閉環 #586 原 bug）。Legacy adapter 仍走原 destructive drain（無 production trigger，#597 追蹤後續對稱化）。
- **#588 reactive host runtime via `SettingsContextFor<'host'>.runtime`**：discriminated union 加 `runtime: HostRuntime | undefined` 必填欄位；HostPage 用兩段式 selective subscription（`preResolveHostId` 算 tentative hostId → `useHostStore((s) => s.runtime[tentativeHostId])` 只訂閱該 host，背景 host tick 不觸發重 render）；ctx 在 `resolvedHostId !== tentativeHostId` 時保守傳 `runtime: undefined`，避免錯誤把 host A runtime 套進 host B 的 disabled(ctx)。HostSidebar 跨 host 切換時優先保留 user 當前 selectedSubPage（若 target host 仍 selectable），否則 fallback 至 target host 第一個 selectable subPage — 消除 runtime-gated 模組情境的 visible blank-and-redirect 閃爍。
- **HostPage isActive side-effect gate**：TabContent 用 `visibility:hidden` 保留非作用中 pane mounted；`lastSelection` 寫回 effect + canonical-path navigate effect 加 `isActive` gate，避免 hidden 實例污染 active 實例的 module-scoped lastSelection、避免 hidden 實例競爭 setLocation。Runtime selector 仍用真實值（不 fabricate undefined）以維持 hidden body 的 disabled(ctx) 評估正確（PR-4 「disabled body 不 mount」契約）。
- **shared `pickHostIdFallback` helper**：把 `preResolveHostId` 與 `resolveSelection.getFallbackSelection` 共用的 fallback 順序（lastSel → activeHostId → hostOrder[0]）抽成單一 source of truth + 等價性測試 14。
- **render-local lastSelection snapshot**：HostPage render 開頭 snapshot 一次傳到所有 helper，避免單 render cycle 內讀到不同 module-scoped 值（多 HostPage 實例 race 的 architectural mitigation；ownership refactor 由 #598 追蹤）。
- 4 輪 codex review 收斂（spec R1 + plan R1 + R1+R2+R3 共 11 findings 修完，R4 standard clean）；3 個延後 follow-up：#596 (HostRuntime DTO 收窄)、#597 (legacy adapter idempotent)、#598 (host-builtin reset 邊界 + lastSelection ownership)。

## [1.0.0-alpha.208] - 2026-04-22

### Revert(lights): rip arbitrator / observation / arbmode stack + trace schema extensions (#594)

- 撤回 Lights 子系統：PR-1a #559（trace schema +10 欄 + `frame_divergences` 表）、PR-1b-0 #564（trace envelope 再 +11 欄 + row class discriminator）、PR-1b-1a #575（observation types + AGENT_ARB_MODE + trace_id strategy）、PR-1b-1b #583（arbitrator goroutine + admission + 9 步 apply pipeline）共四個 merge 全部 revert，-14,577 行 / 56 files。
- 撤回原因：盤點發現 SPA 現行 status 顯示全部走 legacy `internal/agent/{cc,codex,opencode}/status.go` → `AgentEventStore` → WS → `useAgentStore` 已 production 路徑；Lights 是平行建的第二套，main 上 `SubmitObservation` 零呼叫點，對使用者完全透明。繼續推進前發現 per-agent-type status enum / source authority mapping 未定義，架構根基需要重新收斂，決定全面 revert。
- 保留項目：PR-0 #553 的 codex `HasReadiness` capability placeholder（無害、未來可能用到）；legacy hook / probe / sweep 路徑及 `internal/agent/*/status.go` 的 per-agent mapping（production 跑中）。
- 還原 anchor：safety tag `pre-lights-revert-alpha206` → `4348e7bc`（revert 前 origin/main，可一鍵 `git reset --hard` 還原）；archive tag `pr-590-lights-1b-1c-archive` → `96458913`（原 PR #590 的 13 commit 全保存在 origin）。
- PR #590 關閉並刪除遠端分支。相關 worktree / local branch 清理完成。
- 驗證：`go build ./...` 通過、23 個 Go package test 全綠、SPA lint + build 通過、`npx vitest run` 2527/2530 pass（3 fail 在 `src/lib/sync/contributors/hosts.test.ts` 為 pre-existing，與本撤回無關）。

## [1.0.0-alpha.207] - 2026-04-22

### Feat(spa): add untitled editor document flow (#591)

- 新的 in-app 編輯器文件改為先建立在獨立的 `untitled:` URI 上，名稱採 `Untitled`、`Untitled-1` 遞增，並把語言、EOL、encoding 與 untitled 狀態一起留在 editor store。
- untitled rename 改成純前端遷移 pane 與 buffer key，不再誤走後端檔案 rename；第一次存檔則共用 `RenamePopover`，未 rename 的文件先提示 `.txt` / `.md` 建議檔名，已 rename 的文件可直接寫入 `/buffer/<name>`。
- save 後會把編輯中的 untitled 文件遷移到實際的 in-app buffer 路徑，清除 untitled metadata，並同步更新 toolbar / status bar 顯示與相關測試覆蓋。

## [1.0.0-alpha.206] - 2026-04-22

### Feat(spa): HSR PR-4 — Host settings shell + built-in adapter + #541 harness (#582)

- HostPage shell 改 registry-driven：`renderContent` 改用 `listContributions('host').find(c => c.localId === selection.subPage)`，六子頁（overview / sessions / hooks / agents / uploads / logs）透過 pending buffer + dispatch-flushed adapter 註冊為 `_builtin.host.<localId>`，與 PR-2 legacy adapter 同 contract（spec §7.2 dispatch hard constraint）。
- `HostSubPage` 型別從六字面 union 鬆綁為 `string`；`isHostSubPage()` 改為 `listContributions('host').some(c => c.localId === value)` 動態驗證；`HOST_SUB_PAGES` const 保留作 i18n key 參考 + 註冊順序註解（JSDoc 標注非型別來源）。
- Section subtree key：`HostPage.renderContent` 使用 `key={\`${hostId}:${c.id}\`}` 強制跨 host 切換 remount（對稱 PR-3 `${workspaceId}:${c.id}` pattern），防止跨 host 狀態汙染。
- Disabled contribution UX：HostSidebar 不 filter disabled contributions，渲染 disabled row + `disabledReasonKey` title + `data-disabled-ctx="true"` + click no-op；`renderContent` 對 disabled ctx return null（對齊 PR-2 F7 / PR-3 F2）。
- `isSelectable(c, ctx)` + `pickSelectableSubPage(hostId, requested)` helper：三處 fallback（getFallbackSelection / sidebarSubPage / resolveSelection）統一走 selectable 檢查；`lastSelection` read-time clamp 到 live registry；跨 host 切換由 HostPage 透過 helper 重算目標 subPage；disabled/missing URL 自癒 emit `canonicalPath` redirect（R1+R2 六方 codex review 收斂後的統一修法）。
- #541 cross-store rehydrate harness：9 invariant tests 覆蓋 `host-lifecycle.ts` 全部 6 個 `!hostWasRecreated` gate（含 tab restore）；merge-mode `setState` 顯式 reset 所有 mutable store fields（useTabStore.visitHistory / useAgentStore.oscTitles / ccStatus），避免 cross-test leak；PR-1 cascade + undo guards 正確，`host-lifecycle.ts` source 0 改動。
- Commit 1/2/3/4 per-commit spec + quality review 各修 2–4 nits 後 approved；整體 R1 標準 + R2 三視角 parallel（攻擊 / 防守 / 體質）+ R3 verify 共三輪 codex review 收斂，no critical / no P1。
- Follow-ups：#586（dispatch-flushed 第二次 standalone call 會清空 host built-ins，theoretical-risk）/ #587（parseRoute / isHostSubPage 依賴 mutable registry state — architecture discussion）/ #588（disabled(ctx) 動態變化時 body 不 reactive 卸載）均 tracked for PR-5 前處理。#581（disabled built-in self-heal）已由本 PR commit `ca000a81` 修復關閉。

## [1.0.0-alpha.205] - 2026-04-22

### Feat(lights): PR-1b-1b — Arbitrator goroutine + admission + apply pipeline (#583)

- 新 `internal/module/agent/arbitrator/` 套件：單一 goroutine `Run(ctx)` 消費 `Arbitrator.in` (cap 1024)；9 步 apply pipeline（generation gate / watcher / idempotency / pending routing / source priority / monotone lifecycle / single-primary invariant / mode branch / trace emit），含 hook-storm 10ms/50 obs pre-gate（§3.4.2）與 authoritative fail-closed pre-gate（reject 前不修改任何 frameState）。
- `SessionStart` helper：一次完成 force-end old-gen actors（reason=session_restart）+ clear pending + per-obs `SessionRestartCleared` trace + `TraceIDMinter.Mint(sid, newGen)` + `PruneSessionBefore` + `arbmode.Manager.ApplyAtSessionStart()` + synthetic boundary trace（使用**新 mint** 的 trace_id）。
- `TraceWriter` 採 bounded **priority ring buffer** cap 4096（非 FIFO channel）：滿載時 O(n) 淘汰既有最低 DropPriority 項；每 100ms flush 一次走 `TraceStore.AppendSteps` batch。flush 成功才 pop 已送批次，失敗保留 buf 等下輪 retry，不再靜默遺失。
- 新 `TraceStore.AppendSteps([]TraceStep)` API：單 tx 多 chain；chain INSERT OR IGNORE（不存在則建最小 placeholder）+ step INSERT OR IGNORE（idempotent on retry）+ 每個變動 chain 回填 step_count / latest_step_kind / latest_decision / latest_step_reason（COALESCE 保留既有非空值）。
- `frameState` 單 owner in-memory reducer（無 mutex）：per-session generation + actor lifecycle + watcher tokens + IsPrimary；passthrough 與 authoritative mode 都更新，只有 FramesStore / frame_divergences / WS broadcast 按 mode gate（1b-1b 全不寫外部；1b-1c 寫 divergence；Phase 2 寫 frame）。
- `Module.SubmitObservation` admission helper：committed 100ms 有界阻塞 + timeout drop；proposed 非阻塞 drop；滿載 metric `lights_arb_in_dropped{priority}`。
- Shutdown drain：`Run(ctx).Done()` 後繼續 consume inCh 內已排隊 obs 到空才退出，避免關機邊界遺失 committed 事件。
- reconcile tick 5s：flushPendingDue + stale actor detect（30s LastActivity，僅發 trace 不改 status）+ idempotency cache 5 分鐘 TTL prune。stale trace 走 `TraceIDLookup.Get` 取 session/gen trace_id，miss 則不 emit 避免 poison batch。
- #578 修復：`TraceIDRegistry` 拆 `TraceIDMinter` / `TraceIDLookup` 兩介面；具體 struct unexported；constructor 回兩個 interface 值。Arbitrator 收 Minter，hook path（1b-1c）收 Lookup。
- #579 修復：`arbmode.Manager` 改用 `atomic.Pointer[modeTarget]` published snapshot 線性化 `OnConfigChange` ↔ `ApplyAtSessionStart`；最後完成的 publish 保證在下一 SessionStart 被 apply 讀到。
- Plan review 處理：3 P0 / 3 P1 / 2 P2 / 1 P3 全修（atomic published snapshot / priority ring buffer + AppendSteps / frameState both-modes update / SessionStart helper / 1b-1b scope 內移除 retryCh / hook-storm 納入 / idempotency canonical bytes + order-invariant / authoritative fail-closed / interface compile assertion 替代）。
- R1 + R2 三視角 review 處理：C1-C6, C8-C10 全修（boundary trace_id 新 mint / reconcile trace_id lookup / shutdown drain / flush retain-on-error / authoritative pre-gate / committed phase 保留 / stable SpanID UUID / AppendSteps chain summary 回填 / idem prune 接 reconcile tick）；C7 production pending entry trigger 延後追蹤於 #584。

## [1.0.0-alpha.204] - 2026-04-22

### Feat(lights): PR-1b-1a — Observation types + AGENT_ARB_MODE + trace_id strategy (#575)

- 新 `internal/module/agent/observation/` 套件：`Observation` 值型別（11 欄：ActorKey / DecisionPorts / Evidence / TraceID / ObservedAt / SessionID / Generation / ActorID / Source / Kind / ArbMode），`Builder` 採 single-use + deep-copy（DecisionPorts / Evidence / nested slices），第二次 `Build()` panic；`EvidenceRef.Value: any` 為 shallow copy（caller 不得在 Build 後改 Value 內容）。
- 新 `internal/module/agent/arbmode/` 套件：`Manager.Snapshot()` 一次性回傳避免 torn read，不暴露分欄 getter；`env AGENT_ARB_MODE > config.AgentConfig.ArbMode > default("passthrough")`，env 鎖 hot reload；`PUT /api/config` 的空字串 `arb_mode=""` canonicalize 成 `"passthrough"` 再存（R2 共識修正）。
- `TraceIDRegistry` + per-session watermark：`PruneSessionBefore` 推進 watermark；低於 watermark 的 `Mint` 回 `""` + log error，caller 必須 check empty（R2 P2 修正）。
- `ActorKey` 非空要求完整 triple（SessionID + Generation + ActorID 全非空）；zero ActorKey 允許（reconcile 無 proposal 情境，R2 P2 修正）。
- `internal/module/agent/module.go` Init：arbmode build 移到 session check 前，session provider 缺席時 `/api/agent/arbitrator/mode` + `OnConfigChange` 仍可運作（R1 P3 修正）。
- Test：47 observation + 13 arbmode + 7 config/handler；全 repo `-race` clean。
- Review：plan review（3 P0 / 4 P1 / 3 P2 / 1 P3 全修）+ 實作 9 輪 task-level review + R1 標準 + R2 三視角 parallel（4 攻擊 / 3 防守 / 2 體質），共識項全收斂；剩餘延後為 #578（Minter/Lookup interface split）+ #579（arbmode Apply epoch race），於 PR-1b-1b 處理。

## [1.0.0-alpha.203] - 2026-04-22

### Feat(spa): HSR PR-3 — Workspace settings shell + reserved cleanup (#573)

- `WorkspaceSettingsPage` 在 `ModuleConfigSection` 下方新增 registry-driven 區塊：讀 `listContributions('workspace')` 依 `order` 升冪渲染，每個 contribution 收到 `ctx: { scope: 'workspace', workspaceId }`（spec §5.3 rule 4 — ctx 僅由 shell 產生）。
- `register-modules.tsx` 拔掉 reserved `workspace` section（`order: 10`，無 component 的 coming_soon 項）；`SettingsSidebar` 同步精簡：移除 `listReservedItems()` 呼叫、`reservedStart` 分隔線、coming_soon 灰字樣式（reserved cleanup 後皆 dead code），PR-2 F7 的 `disabled(ctx)` 處理保留。
- `settings-section-registry.ts` 拔 `pendingReservedItems` Map、`listReservedItems` export、與 `registerSettingsSection` 的 reserved 分支：舊 API 呼叫方若仍傳 `component: undefined`，改為 `console.warn + return`（非 throw），避免 stale caller / HMR 舊 session 炸 bootstrap（R2 attack F5 修正）。
- **新 finding 收斂**（R1+R2 四路 review）：F1 移除 `WorkspaceSettingsPage` 的 `useMemo([ctx])` 過度快取，`disabled(ctx)` 改為每次 render 重算，store/flag 變化即時反映；F2 workspace shell 對 disabled contribution 改為「顯示 disabled row + `disabledReasonKey`」，與 PR-2 purdex shell 契約一致；F4 section subtree 加 `key={${workspaceId}:${c.id}}` 強制 workspace 切換時 remount，防止 contribution 內 `useState(ctx.workspaceId)` / `useEffect(..., [])` 跨 workspace 狀態汙染。R3 confirmation review clean。
- F3：原規劃拔掉的 `module-config` 全域節 restored — `globalConfig` API 仍 live 但 UI 入口被移除形成 silent dead-end；改為保留 UI，等 PR-5 才真正 deprecate（tracked in #574）。
- 新 `WorkspaceSettingsPage.registry.test.tsx` 7 cases：baseline / render / ctx / order / disabled / workspaceId freshness / cross-scope isolation；`register-modules.test.ts` / `settings-section-registry.test.ts` / `SettingsSidebar.test.tsx` / `SettingsPage.test.tsx` / `settings-contribution-smoke.test.tsx` 同步更新。i18n 移除 `settings.section.workspace` key（`settings.section.modules` 隨 F3 還原保留）。
- Follow-up issues：#574（globalConfig deprecation after PR-5，Backlog）；#538 workspace 層 render-level smoke milestone 可關閉。
- 本 PR 僅含 workspace 層 shell 遷移；`HostPage` 改 registry-driven 與 Editor module 首個用例由 PR-4/5 接手。

### Fix(spa): restore build baseline — MonacoWrapper test missing isActive prop (#576)

- `MonacoWrapper.test.tsx:133` 的 `rerender` 呼叫缺 `isActive` prop（第一次 render 有傳，rerender 漏補），導致 `tsc -b` 自 #570 後在 main red。補上 `isActive={true}` 恢復 `pnpm run build` 全綠。
- Vitest 對型別較寬鬆沒抓到；問題由 HSR PR-3 review 階段發現並獨立以 baseline PR 修復（不夾帶 feature 變更）。

## [1.0.0-alpha.202] - 2026-04-22

### Feat(spa): HSR PR-2 — Purdex settings shell + legacy adapter (dispatch-flushed) (#558)

- `SettingsPage` 的 `GlobalSettingsPage` 與 `SettingsSidebar` 改為 registry-driven，讀 `listContributions('purdex')` + `listReservedItems()`，並向 active component 注入 `ctx: { scope: 'purdex' }`（spec §5.3 rule 4 — ctx 由 shell 產生）。
- `settings-section-registry.ts` 由直接寫 registry 改為 **pending-buffer adapter**：active 項 push 到 `pendingLegacyContributions`（upsert by id），reserved 項寫入 `pendingReservedItems: Map<string, SettingsSectionDef>`（upsert）；`dispatchSettingsContributions()` Phase 2 在 `clearContributions()` 後以 `drainLegacyContributionQueue()` 統一 drain + register，滿足 spec §7.2 dispatch-flushed 硬約束（避免 legacy 被 clearContributions 整批清掉的 regression）。
- 同 commit 完成 #539 收斂：`registerSettingsContribution` / `clearContributions` / `assertValidSettingsContribution` 標為 `@internal`，並以 ESLint `no-restricted-imports` 限定僅 `dispatch-settings-contributions.ts` / `settings-section-registry.ts` / test 可 import 寫入 API；HMR dispose 走單一 `resetSettingsContributionsForHmr()` helper 同清 registry + pending buffer。
- React component identity 透過 `wrapLegacyComponent` + `WeakMap<WrappedComp, OrigComp>` 逆查保留（`toLegacyShape` unwrap 後 `component === 原 passed-in Comp`）。
- 3 輪 codex review（R1 標準 + R2 三路對抗 + R3 確認）全部收斂：R1/R2 HIGH findings（shell 用 localId 當全域 route key / reserved↔active 雙 Map 無互斥清理 / dispatch destructive drain / @internal 無 runtime 護欄）於 F1-F4 commit 修復；R3 P2 findings（localId regex 對不齊 parseRoute / sidebar 未評估 `disabled(ctx)`）於 F6/F7 commit 修復。
- F1：dispatch-time 新增 per-scope localId 唯一性 assertion（同 scope 跨 module 同 localId → throw）；保留 URL `/settings/<localId>` 向後相容。
- F2：reserved ↔ active 互斥清理（每次 `registerSettingsSection` 寫入先刪對方 buffer 同 id 項）；workspace reserved → active 升級（PR-3 動作）不再留殭屍列。
- F3：`peekLegacyContributionQueue()` snapshot + validate-before-commit；dispatch 驗證失敗時 queue 保留供 retry。
- F6：`SETTINGS_LOCAL_ID_RE` 共用 const（`/^[a-z0-9-]{1,32}$/`），registry validator 與 `route-utils.ts` 同源；既有 built-in 10 個 localId 全部 conform。
- F7：單一 `isSelectable()` predicate 貫穿 sidebar / self-heal / initial state picker / handleSelectSection guard / ActiveComponent gate；`disabled(ctx)` 評估 + `disabledReasonKey` i18n tooltip；deep-link 到 disabled section → URL replace 到第一個 selectable。
- 延後 follow-up issue：#563（registry.ts SRP refactor，Backlog；等 PR-5 後 adapter scope 穩定再處理）；#538/#540/#541 持續由後續 PR-3/4/5 解。
- 本 PR 僅含 Purdex 層 shell 遷移；`WorkspaceSettingsPage` / `HostPage` 改 registry-driven 與 Editor module 首個用例由 PR-3/4/5 接手。

## [1.0.0-alpha.201] - 2026-04-22

### Fix(spa): restore lint and build baseline (#556)

- 修正 `main` 上殘留的 SPA lint 與 typecheck 基線失敗，讓 `pnpm --prefix spa run lint` 與 `pnpm --prefix spa run build` 再次可通過。
- 將退化集中收斂到現行型別與 runtime 契約，包含 preview pane、fs backend、sync provider、drag/statusline helper 與多批測試 fixture 對齊。

## [1.0.0-alpha.200] - 2026-04-22

### Test: realign stale fs and editor suites with current runtime (#544)

- 將已失效的 `internal/module/files` list handler 測試遷回現行 `internal/module/fs`，保留目錄排序、hidden file 過濾、empty dir、broken symlink 等仍有意義的 coverage，讓 `make test` 重新可完整跑完。
- `EditorPane` 測試改寫為對齊現行 `FsBackend` editor runtime，覆蓋初始載入、儲存成功、儲存失敗、unmount cleanup 與 active reload，移除對舊 `docId` / `bindingStatus` 模型的錯誤依賴。

## [1.0.0-alpha.199] - 2026-04-22

### Feat(spa): HSR PR-1 — scope-parameterized settings contribution registry + 3-layer stores (#542)

- 建立 Host-scoped Module Settings Registry（HSR）核心：`ModuleDefinition.settings` 以 scope-parameterized discriminated union 宣告，module 可在 `purdex` / `host` / `workspace` 三種 scope 各自掛自己的設定；`AnySettingsContributionDeclaration` distributive union 在 registry / dispatcher / ModuleDefinition 三處 boundary 保留 scope↔ctx 關聯。
- 新增三層 settings store（`useGlobalSettingsStore` / `useHostSettingsStore` / `useWorkspaceSettingsStore`）：persist + BroadcastChannel sync + rehydrate heal 皆就位；heal 拒絕 `__proto__` / `constructor` / `prototype` keys 防 prototype pollution。
- `dispatch-settings-contributions.ts` 以 atomic two-phase（validate → commit）註冊，Invariant I1 保證同 module 同 scope 不與舊 `globalConfig` / `workspaceConfig` 雙軌並存。
- Cascade integration：host 刪除 / workspace 移除會同步清掉對應 scope 的 settings；host 刪除 undo 對 last-host veto、same-id 重建 race、tab restore 綁到新 host 三類邊界 case 都有保護。
- Tear-off / merge 搬移 window 時透過 `removeWorkspace(wsId, { keepSettings: true })` 保留 workspace 設定不被連坐清除。
- 3 輪 codex 4-way review 全部收斂（R3 標準 no findings）；遺留 #538–#541 追蹤 shell migration 後才處理的 MED 級項目（smoke render / internal API 收斂 / store ref 外洩 / cross-store rehydrate order）。
- 本 PR 僅含 registry + 型別 + 三層 store + cascade 整合；SettingsPage / HostPage / WorkspaceSettingsPage 遷 registry-driven 與 Editor module 首個用例（tilde Layer 1/2）由後續 PR-2/3/4 接手。

## [1.0.0-alpha.198] - 2026-04-21

### Feat(agent): add opencode hook integration (#534)

- daemon 新增 `opencode` provider、`pdx setup --agent opencode` 與 `/api/hooks/opencode/*` host-global installer，讓 OpenCode hooks 可比照 Claude Code / Codex 由 Host Hooks 介面統一管理。
- OpenCode plugin 會把 `session`、`permission`、`task` lifecycle 映射成現有 agent hook 事件，並將 `task` tool 對齊為 Claude-compatible `SubagentStart` / `SubagentStop`，補上跨 session pairing、duplicate start 忽略、malformed event 不廣播等保護。
- agent module 補強 OpenCode error guard 與 subagent cleanup：`SessionStart` 會清殘留 subagents、`Stop` 不會誤清 error、`SessionEnd` 在 error 狀態下仍可正常 cleanup。
- Settings 的 Host Hooks 現在會顯示 `OpenCode Hooks` 卡片，並補上 focused Go / SPA tests 覆蓋 provider、installer、subagent contract、detect API 與 page-level hooks UI 接線。

## [1.0.0-alpha.197] - 2026-04-21

### Feat(spa): support ~/... tilde paths in terminal file links (#530)

- Terminal 點 `~/foo.ts` 會以 pane shell `$HOME` 展開（daemon `$HOME` 為 fallback），修正 `~` 被絕對路徑正則 lookbehind 吃掉、點擊後開到 `/foo.ts` 錯檔的 regression。
- Daemon 新增 `GET /api/sessions/{code}/home` 端點：Linux 讀 `/proc/<pid>/environ`、Darwin 走 `ps -E`；pane shell 讀不到時 fallback daemon `HOME`，兩者皆空才回 500。
- Settings「Link 偵測」新增第四個 toggle（預設開）供停用 tilde 偵測；pane home 取得失敗改 fallthrough 用原 raw path 讓 Editor 開空白 buffer，而非 warn+return。
- ESLint config 統一加 `argsIgnorePattern: '^_'`，順手清掉 18 個既有檔散落的 `// eslint-disable-next-line @typescript-eslint/no-unused-vars` 註解。
- 4 輪 Codex review（標準 + 攻擊 / 防守 / 體質）完成；workspace / host 手動 home 不在本 PR scope，遺留由後續追蹤：#531（`~/../foo` 逃出 `$HOME`，刻意保留）、#532（Darwin `ps -E` 遇 env 含空白切壞）、#533（lint config 不該混 feature PR 的 review policy 討論）。

## [1.0.0-alpha.196] - 2026-04-21

### Fix(spa): align sync history settings UI (#528)

- 將 Sync History 頁面從早期 wireframe 風格的硬切 split-pane 收斂回現行 Settings 設計語言，補上正常的 section header、surface card 和一致的狀態樣式。
- History tabs、snapshot rows、detail panel 與 restore dialog 全面改用既有 border / surface / hover token，避免 Sync 子頁在視覺上脫離整體設定頁。
- loading、empty、error 與 warning 狀態一併改成與現行 Settings 相同的面板式呈現，不變動既有 restore 行為。

## [1.0.0-alpha.195] - 2026-04-20

### Feat(agent): add tmux agent hook trace monitor (#526)

- daemon 新增 hook-only trace rail，將 trigger / verify / frame / projection / emit 傳遞鏈持久化到 SQLite，並補上 migration hardening、retention 與 typed monitor APIs。
- Settings 新增 dev-only `Tmux Agent Monitor` 區塊，可依 host / session / pane 檢視 chain list、階層 step tree、selected-step JSON inspector 與 projection summary。
- 前端 monitor 針對 active host、pane-scoped projection、stale request 覆寫與失敗後殘留舊資料等除錯誤導情境補上防護與測試。
- 後續結構性收斂另開 #523、#524、#525 追蹤，不阻擋本次功能出貨。

## [1.0.0-alpha.194] - 2026-04-20

### Fix(agent): harden statusline self-test handshake across version skew (#520)

- Statusline self-test 新增 capability-based `init` / `ready-v1` handshake：新前後端組合會先完成 bus subscriber 就緒，再進入真正的 `proxy -> daemon -> WS -> SPA` 驗證路徑。
- `/api/agent/status` 的 test nonce 仍保留真實 broadcast path，避免 self-test 因為走旁路而失去對 production WS shape / routing 的覆蓋。
- 舊版前端或舊版 daemon 仍可走 legacy fallback，不會因版本錯位把 self-test 永久卡住；SPA 端也把 test bus 命中收斂到當前 `hostId`，避免跨 host 同 nonce 誤判成功。
- 另開 #521 追蹤 `ready-v1` timeout policy 的後續設計，不阻擋本次修正出貨。

## [1.0.0-alpha.193] - 2026-04-20

### Fix(spa): preserve host deep links across tab switches (#518)

- Host 管理頁現在支援 `/hosts/:hostId/:subPage` deep link，切換 tab 後回到 hosts 頁時會保留上一個 canonical host section，而不是跳回第一個 section。
- `HostPage` 改成 URL-driven selection；bare `/hosts`、invalid deep link、host deletion fallback 都會收斂到可預期的 canonical path，且在沒有 host 可選時會回到 bare `/hosts`。
- `useRouteSync()` 對 valid / invalid host deep links 都只開 singleton hosts tab，不會把 host selection 滲進 tab model。
- Host route 會對 `hostId` 做 URL encode/decode，避免含保留字元的 host id 破壞 deep link。
- 補齊 parser、`HostPage`、mounted route-sync 測試，涵蓋 remount、keep-alive、invalid route、no-host fallback 與 canonicalization。

## [1.0.0-alpha.192] - 2026-04-20

### Feat(sync): P1 Sync History + Restore (#488)

- **SnapshotStore on IndexedDB**：`lib/storage/idb.ts` 版本感知 cache（rejected / terminated / blocking 時重連），`features/sync/snapshot-store.ts` 提供 `init / create / get / list / delete / compact / demoteSessionPristine / rotateSessionPristine / clear`，`list` 僅回 metadata，tiered compaction 每層保留最新（monthly bucket cap 12）。
- **Restore contract**：`useSyncStore.restoreFromSnapshot(snapshot, source, options?)` 引入 `PreOpFailedError / SnapshotCoverageError / RestoreFailedError` 與 `RestoreOptions (skipPreOp, allowMissingContributors)`；snapshot 需涵蓋完整 contributor registry，進入 deserialize 前清 `pendingConflicts` 避免 partial-restore 後 stale conflict 覆寫 restored 資料；`restoreTokenRef` 隔離 in-flight restore 不跨 row 污染。
- **Safety net**：`createPreOperationSnapshot` 與 `ensureSessionPristine` 序列化完整 contributor registry（不是 `enabledModules`）；pristine rotation 走單一 IDB readwrite transaction，跨 tab 也能收斂到恰好一份 pristine；quota retry：`createSnapshot` put 失敗 → compact → retry 一次。
- **Auth boundary**：`contributors/hosts.ts` full-replace 時僅在 endpoint `(ip, port)` 完全一致才保留 token，否則強制 re-auth，避免惡意 bundle 將 bearer token 重指向攻擊者 daemon。
- **UI**：雙層路由 `/settings/<section>/<subsection>` + `SettingsRouteContext`；`SnapshotHistoryPage` + `HistoryTabs` + `HistoryList` + `SnapshotDetail` + `SnapshotRestoreDialog`（confirm / preOpFailed / coverageWarning 三模態，override 跨 retry 累積）；dialog Cancel 在 restore in-flight 時 disabled，阻斷並行 restore。
- **Integration**：`manual-provider.ts` 匯入前建立 pre-import snapshot（失敗不阻斷匯入）；`App.tsx` bootstrap 呼叫 `ensureSessionPristine`（rejection 已 catch）；i18n 新增 45+2 key（en + zh-TW）。
- **Scope**：daemon `/api/sync/snapshots` + Remote tab 接線留給 PR B；Remote tab 目前對 daemon user 顯示占位。
- **Known issues（後續 PR 處理）**：[#492](https://github.com/wake/purdex/issues/492) · [#493](https://github.com/wake/purdex/issues/493) · [#494](https://github.com/wake/purdex/issues/494) · [#495](https://github.com/wake/purdex/issues/495) · [#503](https://github.com/wake/purdex/issues/503) · [#510](https://github.com/wake/purdex/issues/510) · [#511](https://github.com/wake/purdex/issues/511) · [#512](https://github.com/wake/purdex/issues/512) · [#514](https://github.com/wake/purdex/issues/514)。

## [1.0.0-alpha.191] - 2026-04-20

### Feat(agent): identity and liveness convergence phases 1-7 (#489)

- **Hook provenance v2**: `pdx hook` payload 現在攜帶 `tmux_pane_id`、`sender_pid`、`sender_start_time`、`sender_uncertain`，daemon 端只接受新 schema，並驗證 hook 送進來的 pane / PID / start time provenance。
- **Frame-authoritative state**: 新增 `agent_frames` store，session 狀態改由 frame projection / replay / sweep 驅動；accepted v2 hooks 不再 dual-write `agent_events`，startup 先 sweep，再 replay，避免 ghost session resurrection。
- **Process identification**: provider 改吃 `ProcessInfo`，`probe.IsAliveFor()` 走 PID tree + `Identify(ProcessInfo)`，不再依賴 command-name / `pane_current_command` 舊路徑；legacy liveness handlers 已移除。
- **Activity convergence**: activity watcher 收斂為 `waiting` / `running` / `idle` 三規則，加入 shell prompt 檢測與 `capture-pane -e` 路徑；`shell_prompt + dead PID` 現在會正確觸發 sweep / clear。
- **Docs and follow-up**: convergence spec / plan 已同步到最終實作；剩餘 replay / snapshot legacy fallback duplication 另開 #505 追蹤。

### Chore(spa): flag-gated debug logs for statusline self-test chain (#506)

- `spa/src/lib/statusline-test-debug.ts` — 以 `localStorage.getItem('pdx:debug:statusline-test') === '1'` gate 的 `debugStatuslineTest` helper，no-op when flag absent。
- Instrumented points（只對 `__pdx_test_*` sessions fire）：`ws.entry`（`useMultiHostEventWs`）、`dispatch.entry` / `.parsed` / `.emit-bus` / `.reject.*`（`agent-ws-dispatch`）、`bus.subscribe` / `.emit` / `.unsubscribe`（`statusline-test-bus`）、`hook.stage1-passed` / `.bus-callback` / `.stage5-check` / `.early-hit-check` / `.sse-outcome` / `.grace-start` / `.grace-result`（`useStatuslineTest`）。
- 用途：本地 triage stage 4 `"WS event not received"` 真因時，整條 daemon → WS → dispatcher → bus → hook 的 timeline 可視化，pinpoint 哪一層斷掉。

## [1.0.0-alpha.190] - 2026-04-20

### Fix(spa): statusline test — stage 4 grace window for late WS events (#490)

- **Root cause**：SSE `done` 比 WS `agent.status` 先到 SPA，舊版 `unsubBus` 立即執行 → 後到的 WS 沒人接，stages 4/5 卡 `running` 轉圈到天荒地老。舊 8s overall timeout 救不到（`work` 已 resolve 走 `done` 路徑）。
- **Fix**：新增 `STAGE4_GRACE_MS = 2000` 的 post-SSE grace window，grace 超時 → `unsubBus` → `markFailThenSkipRest(4, "WS event not received within 2000ms after stream completed")`，stage 5 連帶 skipped。既有 `StatuslineTestPanel` log 按鈕自動顯示 error。
- **State machine 清理**：`stage4Awaiting` 與 `stage4Resolver` 合併為 `Stage4State` union（`pending` / `armed` / `fired` / `cancelled`），消除雙 flag 隱性依賴；`markFailThenSkipRest` 不再能蓋掉已 fired 狀態。
- **Early-hit 修正**：命中後立即 `unsubBus`，避免後到 WS 觸發冗餘 subscriber。
- **Doc**：`OVERALL_TIMEOUT_MS` comment 註明 grace 不在 overall 內，整體最壞 8+2=10s。
- **Tests**：新增 `SSE done without WS event → stage 4 fails after grace period`（fake timers）；既有 happy-path / stage-1 failure / overall-timeout 全綠（5 passes）。2158 個 SPA 測試全綠。
- **Follow-ups**：開 #496（StrictMode ref reset）、#497（error 字串 i18n）、#498（dispatcher↔bus contract test）、#499（SSE 亂序防禦）、#500（state machine 抽離）追蹤。

### Feat(spa): Codex icon variant setting (#491)

- **Settings → Terminal → Codex icon**: OpenAI / Codex monochrome button pair with live preview, mirroring the existing Claude Code bot/star row. Active button uses `aria-pressed`; when tab indicator is dot-only, a hidden-hint paragraph matches the CC behaviour.
- **Icon options**: `openai` (Phosphor `OpenAiLogo`, default — unchanged behaviour) and `codex` (monochrome `@lobehub/icons-static-svg/codex.svg`, inherits tab theme via `currentColor`).
- **A11y**: `aria-hidden="true"` on the agent-icons wrappers so lobehub SVG `<title>` elements no longer pollute button accessible names.
- **Tests**: +3 in `agent-icons.test.tsx` (codex variant coverage), +1 in `TerminalSection.test.tsx` (codex button click), +locale-completeness keeps en/zh-TW in sync. Full suite 2161/2161.

### Refactor(spa): move tab/icon preferences to useUISettingsStore

Driven by codex review findings on PR #491. Previously these preferences
lived in `useAgentStore` alongside runtime state, so any schema bump would
silently reset user settings — and the new `codexIconVariant` would not
have roamed through the preferences sync contributor.

- **Moved**: `tabIndicatorStyle`, `ccIconVariant`, `codexIconVariant`, `showOscTitle` (types, state, setters) from `useAgentStore` → `useUISettingsStore`.
- **Migration**: `useUISettingsStore` v1 → v2 with a `migrate` that imports the old `purdex-agent` payload's UI pref keys when present, so upgraders keep their settings.
- **Sync**: `preferences` sync contributor's `DATA_FIELDS` now includes the four moved keys — they roam across devices and survive import/export.
- **Store**: `useAgentStore` no longer persists anything and drops the `persist` middleware + `syncManager.register` entirely. It's now pure runtime state.

## [1.0.0-alpha.189] - 2026-04-19

### Fix(agent): probe wrapped descendants with bounded cache (#484)

- **Wrapped agent liveness**: `probe.IsAliveFor()` 保留前景 command 與 direct-child 快路徑，之後補上 recursive descendant 偵測，修正 `shell -> node -> codex` 這類包裝程序被誤判成 dead 的問題。
- **Bounded cache**: descendant 結果快取現在綁定 tmux target、pane PID、direct-child snapshot，並加上短 TTL；同 target 換 pane、grandchild 變動或 cache 過期都會重查，避免把 stale alive/dead 判斷釘住。
- **Probe-local capability**: recursive descendant 查詢改為 probe 內部的 optional capability，不再把 `PaneDescendantCommands` 掛進全域 `tmux.Executor` 介面。
- **Tests**: 新增 wrapped descendant、TTL 內命中、TTL 後 grandchild 變動、pane PID 變更等 probe regression tests；`go test ./internal/agent/probe ./internal/tmux ./internal/module/agent` 通過。

## [1.0.0-alpha.188] - 2026-04-19

### Feat(spa): Extensions sub-row status icon (#480, PR #482)

- 13px Phosphor status icon left of the Status integration label in `AgentExtensionRow`:
  - `pdx` / `wrapped` → `CheckCircle` (green-400)
  - `unmanaged` → `WarningCircle` (yellow-400)
  - `none` → omitted
- Icons marked `aria-hidden="true"`; the existing text badge already conveys state to screen readers.
- 4 new TDD tests covering all 4 modes + icon type + a11y attribute.

### Feat: Statusline pipeline self-test panel (#481, PR #483)

- **One-click loopback self-test** under Status integration validating the full `proxy → daemon → WS → SPA store → UI` chain without spawning `claude -p`.
- **Daemon** (Go)
  - `POST /api/agent/cc/statusline/test` SSE endpoint streams per-stage pass/fail for stages 1-3 (proxy spawned / daemon POST received / daemon WS broadcast).
  - Test nonce `__pdx_test_<8-hex>` rides through the real `pdx statusline-proxy` subprocess via new `PDX_STATUSLINE_TEST_SESSION` env override.
  - `handleAgentStatus` detects nonce prefix → signals per-nonce observer channel → broadcasts WS keyed by nonce → never persists to `statusSnapshots` or hits `resolveSessionCode`. Phantom-tmux-session-free.
  - Targeted `agent.status.cleared` (scoped by session) for cleanup — distinct from the existing unscoped (empty session) wipe-all semantics used for statusline uninstall.
  - `testSpawnProxy` seam for unit tests; `defaultSpawnTestProxy` uses `os.Executable()` + `filepath.EvalSymlinks` + 2s context timeout.
- **SPA** (React / TypeScript)
  - `useStatuslineTest(hostId)` hook: POSTs, parses SSE for stages 1-3, subscribes to `statuslineTestBus` for stage 4 (WS event received), introspects `useAgentStore.ccStatus` for stage 5 (store populated). 8s overall client timeout with `reader.cancel()` on timeout and `runningRef` re-entrancy guard.
  - `StatuslineTestPanel` renders 5-node status with Phosphor icons (`CheckCircle` / `XCircle` / `Minus` / `CircleNotch`), Run-again button, expandable failure log.
  - `AgentExtensionRow` embeds the panel when `state.mode` is `pdx` / `wrapped`; panel's internal `autoRanRef` + mount/unmount cycle auto-runs exactly once per install.
  - `agent-ws-dispatch`: nonce-aware routing — `agent.status` with `__pdx_test_` session also emits to the test bus; `agent.status.cleared` with non-empty session routes to scoped `clearSession(hostId, nonce)`.
  - 14 new `hosts.extensions.test.*` i18n keys (en + zh-TW).
- **Review fixes**: timeout 5s → 8s (server worst case is 6s), dangling SSE reader `reader.cancel()`, re-entrancy guard on `run()`, `EvalSymlinks` for symlinked binary installs.

### Follow-up issues

- #480 ✅ addressed by PR #482
- #481 ✅ addressed by PR #483
- Deferred observations (low priority): auto-run on panel remount (by design — fresh install remounts), discarded `cmd.Stderr` in test spawn (stage-1 failure shows only exit code), synthetic `ccStatus[host:nonce]` lingers if SSE connection drops before cleanup (harmless, bounded by page lifetime)

## [1.0.0-alpha.187] - 2026-04-19

### Fix(agent): Codex hook schema compatibility + hook support version warning (#479)

- **Codex hooks schema**：`~/.codex/hooks.json` 改寫為 Codex 0.121+ 需要的 matcher-group schema（`{"hooks": [...]}`），避免 legacy direct-entry 看似已安裝但實際完全不觸發。
- **Legacy detection scoped to pdx**：`CheckHooks` 偵測 legacy direct-entry 時，只對 pdx 自己的 command 觸發「reinstall required」；第三方 legacy hook 與 pdx matcher-group 並存時不再誤報。
- **Agent version cache**：`DetectHookAgentVersion` 加 60s per-binary TTL cache。原本每次 `CheckHooks` 都同步 spawn `claude --version` / `codex --version`（含 5s timeout），現在最差情況降為每分鐘一次。
- **Hook version support fields**：hook status API 新增 `agentVersion` / `supportedVersion` / `exceedsSupport`，目前記錄已驗證的 Claude Code（2.1.114）/ Codex（0.121.0）hook 支援上限。
- **Hosts UI**：Hook 卡片顯示 Agent 版本與 Hook 支援上限，agent 版本高於支援版本時顯示黃色警示（`<WarningCircle>` + `hosts.hook_version_warning` key）。
- **Tests**：新增 version parser/compare 測試、version cache TTL 測試、Codex legacy 格式 regression test（含 pdx 與第三方 legacy 並存情境）、HookModuleCard 版本資訊顯示測試。

## [1.0.0-alpha.186] - 2026-04-19

### Feat: Statusline installer UI (PR-1c, #473)

- SPA installer UI for CC statusline extension under Host → Agents. CC card gains Extensions region with Install / Remove affordance; Codex card has no Extensions section.
- `useStatuslineInstall` hook wraps `GET /api/agent/cc/statusline/status` + `POST /api/agent/cc/statusline/setup`; exposes `{ state, phase, error, install, remove, refresh }` with per-effect cancelled closure for hostId-change safety.
- `StatuslineConflictDialog` prompts user when CC has an unmanaged statusLine — Wrap route POSTs `{ action: install, mode: wrap, inner: rawCommand }`. Full a11y: `role=dialog` + `aria-modal` + `aria-labelledby`; Escape / backdrop / stop-propagation dismiss.
- `AgentExtensionRow` gates button choice by `state.mode` (not `installed`): `none` / `unmanaged` → Install, `pdx` / `wrapped` → Remove. Remove confirmation via `window.confirm`. Buttons disabled when host offline (matches `HookModuleCard` pattern).
- 14 new `hosts.extensions.*` i18n keys (en + zh-TW).
- Pre-merge round-2 review fixes: `setError(null)` before mutation fetches, stale hostId fetch cancellation, offline button guard, test comment clarification.

### Follow-up issues

- #474 add fetch timeout to `useStatuslineInstall`
- #475 `ConflictDialog` should dismiss on `hostId` change
- #476 extract `ConfirmDialog` to replace `window.confirm`
- #477 `AgentExtensionRow.extensionId` prop is false extensibility
- #478 `AGENT_EXTENSIONS` config table instead of `agentType === 'cc'` branching

## [1.0.0-alpha.185] - 2026-04-19

### Feat: terminal link detection — 3 modes (absolute / relative with `/` / bare filename), gated by settings（#470）

- 將內建 file-path matcher 拆為 3 個獨立 matcher（絕對路徑 / 含 `/` 的相對路徑 / 無 `/` 的裸檔名），各自對應 `linkDetectAbsolute` / `linkDetectRelativeSlash` / `linkDetectBareFilename` 設定開關（預設僅 absolute 開啟，其餘因高誤判率預設關閉）。
- 同時修正所有 matcher 的 multi-extension 錯配 bug：`foo.d.ts` / `file.min.js` / `v1.2.3.tar.gz` 原本只匹配第一段副檔名，現以 `(?:\.[A-Za-z0-9]+)+` 全數納入。
- `TerminalSection` 新增 `<LinkDetectionSection>` 子元件（3 toggle + i18n，en + zh-TW），與 appearance 設定解耦。
- Matcher 採 `createFilePathMatcher({ id, regex, isEnabled })` factory 樣式，gate 由 `register.ts` 在註冊時注入，解除對 `useUISettingsStore` 的直接耦合，unit test 不再需 mutate store。

### Fix: terminal link underline no longer drifts on lines with CJK / wide chars

- 新增 `buildJsOffsetToCol`（`col-map.ts`）：走訪 xterm `IBufferLine.getCell` 建立 JS UTF-16 offset → terminal cell column 的單調遞增表，binary search 查詢；正確處理 CJK 寬字元、emoji surrogate pair、past-end clamp。
- `createXtermLinkProvider` 每次 `provideLinks` 建表一次供所有 matcher 共用，matcher 回傳的 JS offset 統一在 provider 層轉為 terminal cell column 再建構 `ILink.range`，修正連結底線偏移。
- `LinkRange` 型別註解更新，明確雙層語意：matcher emit JS offset / dispatch 到 opener 時 `token.range` 已為 terminal cell column。

### Feat: daemon `GET /api/sessions/{code}/cwd` — returns tmux `pane_current_path` for link resolution

- `tmux.Executor` 新增 `PaneCurrentPath(target)`，呼叫 `tmux display-message -p -t <target> '#{pane_current_path}'`；`FakeExecutor` 對應 `SetPaneCwd` setter。
- 新增 `GET /api/sessions/{code}/cwd` 路由，handler 先 `GetSession(code) → info.Name` 解碼再查 tmux（對齊其他 handler 模式）。
- SPA 新增 `fetchSessionCwd(hostId, sessionCode, signal?)` host-api client。`BuiltinTerminalLinksDeps` 由 8 欄位扁平結構重組為 `{ urlOpener, filePathOpener }`，sub-object 與各自 `createOpener` 參數 1:1 對應。

### Fix: file-path opener security — path traversal guard + IP/version false-positive filter + 5s timeout

- `resolveCwdPath(cwd, rel)` 正規化 `.` / `..` / 重複斜線後驗證結果仍在 `cwd` 的父目錄範圍內；`../../../etc/passwd` 類攻擊被拒絕，並以 `console.warn` 記錄。允許一層 `../App.tsx` 的常見相對路徑。
- Matcher 新增「全副檔名皆為數字」的排除（如 `192.168.1.1` / `1.2.3` / `1.5`），避免 IP 位址與版本號誤判為檔名。
- Opener cwd fetch 加 5 秒 timeout（AbortController），慢/斷線 host 不再 ghost click 無回饋；所有失敗路徑改為 `console.warn` 帶 host/session/path 上下文，非靜默。

### Follow-up issues

- Daemon `fs/handler.go` 僅做 `filepath.IsAbs(filepath.Clean(path))` 驗證，無 cwd boundary 檢查；SPA 為單一防線。此為既有架構，非本 PR 引入，另開 issue 追蹤。

## [1.0.0-alpha.184] - 2026-04-19

### Feat: CC statusline installer — SPA ccStatus store + tab rendering（PR-1b，#471）

- 新增 `<HoverTooltip>` 元件（`spa/src/components/HoverTooltip.tsx`）：從 `ActivityBarNarrow` 的 `ws-tooltip` pattern 抽出，純 CSS `group-hover:opacity-100` fade-in，`placement: 'top' | 'right'`，`role="tooltip"`，parent 需 `.relative.group`。`ActivityBarNarrow` + `InlineTab` + `SortableTab`（pinned + regular）皆改用之。
- `useAgentStore`：新增 `ccStatus: Record<compositeKey, CcStatusEntry>` 非 persist slot，加 `setCcStatus` / `clearHostAgentStatus` actions。`setCcStatus` 將 `session_name` 鏡射到現有 `oscTitles` channel（經 `sanitizeOscTitle` 清理 ANSI/C0）。`clearHostAgentStatus` 按 provenance 只清除 ccStatus 來源的 oscTitles，保留終端 OSC 0/2 來源的 titles。抽出 module-level `omitKeys` helper，`removeHost` 共用。
- `dispatchAgentWsEvent`（`spa/src/lib/agent-ws-dispatch.ts`）：新 pure helper，由 `useMultiHostEventWs` 呼叫。解開 daemon wire shape `{agent_type, status}`、驗證 `agent_type === 'cc'`、把 inner `status` 傳給 `setCcStatus`。`agent.status.cleared` 呼叫 `clearHostAgentStatus`。非物件 JSON / 錯 agent_type / 缺 status 欄位皆靜默忽略。
- `useTabDisplay.displayTitle` 改為 `${oscTitle} - ${baseLabel}` 組合形式（原本 oscTitle only）；`tooltip` deprecated 欄位整個移除，InlineTab + SortableTab 統一從 `displayTitle` 取值。
- `InlineTab` 改用 `<HoverTooltip placement="right">`（避開 `ActivityBarWide.overflow-y-auto` 截斷）；`SortableTab` 兩個 call sites（pinned button + regular span）也遷移到 HoverTooltip（placement=top）。
- 兩輪 review + 大量 pre-merge fix：round-1 clean；round-2（attacker / defender / file-size）找到 3 個 user-facing bug（wire shape mismatch 導致 feature 整個不運作、tooltip placement 被 sidebar 截斷、a11y regression）全部 pre-merge 修；defender 提出的 oscTitles provenance concern 也順手修；filterKeys 重複 + deprecated tooltip 欄位一併清掉。
- 測試：2065/2065 pass（+10 vs PR-1a baseline），204 files，`tsc --noEmit` clean。

### Follow-up issues

- #472 HoverTooltip 擴充 `placement='bottom'`（SortableTab 在 top TabBar 用 `placement='top'` 是 compromise，實測若 clipping 明顯再處理）

## [1.0.0-alpha.183] - 2026-04-19

### Feat: CC statusline installer — wrapper + daemon endpoints（PR-1a，#464）

- 新增 `pdx statusline-proxy` subcommand：讀 stdin JSON、render 預設 `[pdx]` 或透過 `--inner` exec 原 CC statusLine 指令、同步 POST 狀態到 daemon（2s timeout，靜默失敗）。`resolveDaemonHost` 把 `0.0.0.0`/`::`/空字串重寫為 `127.0.0.1`，避免 daemon bind 萬用位址時 proxy 無法連線。
- `internal/agent/cc`：`StatuslineInstaller` interface 實作，install / remove 對 `~/.claude/settings.json` 做 atomic write（保留 file mode、tmp-leak-safe），shell-quote round-trip（pdxPath、inner 皆單引號包覆），mode 偵測（none / pdx / wrapped / unmanaged）用 `go-shellwords` 解析；remove 拒絕 unmanaged 以 409 回報。
- `internal/module/agent`：`GET/POST /api/agent/{agent}/statusline/setup` + `GET /api/agent/{agent}/statusline/status` + `POST /api/agent/status`（收 proxy POST）+ WS `agent.status` snapshot replay on subscribe + `agent.status.cleared` broadcast on remove。`statuslineMutex` 序列化 RMW 並於 HTTP 回應前釋放；`statusSnapshots` 狀態掛在 `Module` struct（非 package global）。
- 兩輪 review + 5 個 pre-merge fix：round-1（5-angle）修 `pdxPath` shell-quote；round-2（attacker / defender / file-size）修 `bind=0.0.0.0` 靜默失敗、`os.Exit(0)` 一致性、`statuslineMutex` 包住 HTTP response、snapshot 狀態搬進 `Module`、`snapshotMu` 在 WS `sub.Send` 前釋放。
- 測試：Go ~50 tests（proxy render / shellwords / install / remove / WS snapshot / handler）全綠。

### Follow-up issues

- #465 Statusline handler 硬編碼 `agentType != "cc"` guard（Codex 擴展時需統一為 registry + interface assertion）
- #466 `removeStatusline` 雙重讀取 `settings.json` 存在 TOCTOU（可靜默刪除使用者 unmanaged 設定）
- #468 `hooks.go` 與 `statusline.go` 的 `settings.json` 讀/寫流程重複（hooks 那份缺 file mode 保留）
- #469 `handler.go` 641 行 / 5 職責，PR-1c 前建議拆分 `handler_statusline.go` / `handler_hooks.go`

## [1.0.0-alpha.182] - 2026-04-19

### Feat: terminal link registry — matcher/opener plugin architecture（#458）

- 新增 `spa/src/lib/terminal-link/` 模組：`LinkMatcher`（偵測 token）+ `LinkOpener`（處理點擊）可獨立註冊的 registry 架構；dispatch 以 priority DESC 路由。
- 內建 URL matcher（`http(s)://…`，含結尾標點剝除、URL query path 誤匹配排除）+ URL opener（Electron：browser tab / shift+click mini window；web：`window.open`；加 http(s) scheme 白名單兜底）。
- 內建 file-path matcher（絕對 Unix 路徑 + 末段需副檔名 + 可選 `:line[:col]`；regex 改寫避免 ReDoS）+ file-path opener（橋接既有 `FileOpener` registry，Editor / Image / PDF 既有 opener 零修改自動套用）。
- xterm 整合：新增 `createXtermLinkProvider` 將 registry 包成 `ILinkProvider`，於 `useTerminal` 以 `registerLinkProvider` 掛載；`linkContext` 透過 ref 傳入讓 mount-only effect 不需重綁。
- 移除 `WebLinksAddon` + 舊 `spa/src/lib/link-handler.ts`；boot 在 `registerBuiltinModules()` 透過 DI 接上 Tab/Workspace store + Electron API。
- 測試：181 tests（registry / matcher / opener / xterm-provider / register / TerminalView 整合）全綠，含 ReDoS 防護 + scheme 白名單 + dispose lifecycle 驗證。

### Follow-up issues

- #462 Electron `openMiniWindow` API 版本不匹配缺 fallback
- #463 host switch 時 activate token 可能 stale

## [1.0.0-alpha.181] - 2026-04-19

### Feat: daemon self-rebuild + server-side `PDX_DEV_UPDATE` gate（#456）

- 新增 `POST /api/dev/daemon/rebuild`（SSE）：TryLock → `go build` 帶 `-ldflags -X BakedInHash=<hash>` → atomic rename `bin/pdx.new → bin/pdx` → `syscall.Exec` self。build context 繼承 `m.stopCtx`，SIGTERM 會取消進行中 build。
- 新增 `GET /api/dev/daemon/check`：回 `{current_hash, latest_hash, available}`，`current_hash` 由 `-ldflags` 在 build 時注入（Makefile 已更新）。
- 新增 listener `SO_REUSEADDR` + 指數退避 retry bind（最後一圈不 sleep），survive exec-self restart。
- `/api/dev/update/*` + `/api/dev/daemon/*` 補上 server-side `PDX_DEV_UPDATE=1` gate（原本只在 Electron preload 端 gate，是裸露洞）；註冊雙層 gate 註解 + `Start` disabled log 升級說明 `config.Dev.Update` + env var 兩條件。
- SPA `Settings → Development` 新增 Daemon 區塊：check / rebuild 按鈕、即時 SSE log 面板、rebuild 成功 3s 後自動重 check；i18n keys `settings.dev.daemon.*`（en + zh-TW）。
- 測試：38 Go tests（含 race）覆蓋 gate、listener rebind、check、rebuild success / error / 409 / rename-failure；SPA 1906 tests 全綠。

### Follow-up issues

- #459 SPA `rebuildDaemon` 缺 AbortController + setTimeout cleanup
- #460 `go run` 啟動時 rebuild 靜默失敗偵測
- #461 `DevEnvironmentSection` 拆出 `DaemonRebuildSection`

## [1.0.0-alpha.180] - 2026-04-19

### Feat: active workspace/home 標題點擊改 toggle 展開（#457）

- `WorkspaceRow` / `HomeRow`：當該列已 active 且 `tabPosition !== 'top'` 時，點擊標題切換 inline tabs 展開/收合（不再 re-select）。
- 非 active 時維持原本 select 行為，chevron 按鈕一律 toggle。

### Fix: 從 Home 切到空 workspace 卡住 standalone tab（#457）

- `handleSelectWorkspace` 在目標 ws 沒 tabs 時補 `setActiveTab(null)`；先前 standalone tab 留著讓 `activeStandaloneTabId` 繼續遮蔽 `ActivityBar` 的 `isActive`，visual 看起來沒切過去。
- Pre-existing bug，被 active-click toggle 誘發後才注意到。

## [1.0.0-alpha.179] - 2026-04-19

### Fix: Activity bar resize handle 無法拖曳（#455）

- `ActivityBarWide` 包 `RegionResize` 的 wrapper 從 `hidden lg:block` 改為 `hidden lg:flex`。
- 原本 `.relative.w-px` 是 block-in-block，高度塌成 0，`inset-y-0` 熱區跟著變 0 高，看似 handle 存在實則點不到。
- 改成 flex 容器後內層以 flex item stretch 填滿，熱區恢復正常可拖。

## [1.0.0-alpha.178] - 2026-04-18

### Tweak: TitleBar 按鈕下移幅度 2px → 2.5px

- `sidebar-toggle` / `layout-buttons` 兩個 slot 的 `translate-y` 從 2px 微調到 2.5px。
- 直接 main commit（微調，無 PR）。

## [1.0.0-alpha.177] - 2026-04-18

### Tweak: TitleBar 按鈕下移幅度 3px → 2px

- `sidebar-toggle` / `layout-buttons` 兩個 slot 的 `translate-y` 從 3px 再降到 2px。
- 直接 main commit（微調，無 PR）。

## [1.0.0-alpha.176] - 2026-04-18

### Tweak: TitleBar 按鈕下移幅度 5px → 3px

- `sidebar-toggle` / `layout-buttons` 兩個 slot 的 `translate-y` 從 5px 降到 3px，視覺上稍微往上靠一點。
- 直接 main commit（微調，無 PR）。

## [1.0.0-alpha.175] - 2026-04-18

### Polish: TitleBar 按鈕視覺收斂 (#454)

- `CollapseButton` topbar variant 拿掉多餘的 `flex items-center justify-center`，class string 與 TitleBar 右側 region-toggle 完全一致（svg `display: block` 本來就能自然置中）；header-right / divider 變體保留 flex 置中（它們有固定 w/h）。
- `TitleBar`：`sidebar-toggle` 與 `layout-buttons` 兩個 slot 加上 `translate-y-[5px]`，整體下移 5px，不再與 traffic-light row 視覺互衝；title 與 sync 警示（absolute overlay）不動。
- 測試：CollapseButton 補 3 組 class-shape assert（topbar 無 flex / header-right + divider 保留 flex）；TitleBar 補 2 組 slot 有 `translate-y-[5px]` 的 regression。

## [1.0.0-alpha.174] - 2026-04-18

### Fix: `tabPosition='both'` 允許折疊 activity bar (#453)

- #452 改了 icon + 樣式但沒解掉核心 bug：`both` 模式下 `CollapseButton.locked` 永遠為 true，按下沒反應、tooltip 還誤寫「tabs are on the left」。
- 根因：`locked` / store guards / heal invariant 把 `left` 和 `both` 綁在一起要求 wide。實際上 `both` = 上方 tab bar 已經可以觸及所有 tab，activity bar 縮窄完全合理。
- `CollapseButton.locked` 收窄到 `tabPosition === 'left'`；`setActivityBarWidth` / `toggleActivityBarWidth` / `setTabPosition` / `healLayoutInvariant` 都拿掉 `both` 的強制 wide。
- 現有 both+wide 的 persisted layout 不動，只是從現在起可以自由 toggle。
- 測試：`useLayoutStore.test.ts` + `CollapseButton.test.tsx` 改寫成新規格並加入 "toggle wide→narrow in both" 與 `setTabPosition` 保留寬度兩組新案例。

## [1.0.0-alpha.173] - 2026-04-18

### Tweak: Activity-bar collapse button 換成 SidebarSimple (#452)

- `CollapseButton` 改用單一 `SidebarSimple` icon，取代動態 `CaretDoubleLeft` / `CaretDoubleRight` 兩張圖互換。
- `topbar` variant 加上 region-toggle 慣用的 active 配色：wide 時 accent 底色（讀作「activity bar is on」），narrow 時 secondary 常態，和 TitleBar 右側 region toggles 同一家族。
- 單一 icon 消除每次點擊的 swap flicker，連帶掃除 #446 commit message 裡提到的「感覺壞掉」觀感。
- 測試面：`CollapseButton.test.tsx` 補上 SidebarSimple path 簽章、topbar active/inactive 配色、與 `p-1 rounded` 共用 pattern 的三組 regression。

## [1.0.0-alpha.172] - 2026-04-18

### Cross-platform: sub-pixel tab icon margin 在 @1x 退回整數 (#451)

- 新增 Tailwind 4 custom variant `lowdpi (@media (max-resolution: 1.5dppx))` 在 `spa/src/index.css`，covering @1x 與 Windows fractional scaling < 1.5x DPR。
- `TabIcon.tsx` / `renderInlineTabIcon.tsx` 兩處 sub-pixel margin 補上 fallback：
  - 非 badge wrapper：`ml-[1.5px] lowdpi:ml-px`
  - badge wrapper：`mr-[0.5px] lowdpi:mr-0`
- 編出的 CSS 為 `@media (resolution<=1.5x){.lowdpi\:mr-0{...}}`，避免 @1x 螢幕上 0.5px 被瀏覽器不一致 round 造成 tab icon 抖動或對不齊。

## [1.0.0-alpha.171] - 2026-04-18

### Tweak: Tab icon margin 依 indicator style 微調 (#450)

- **icon / dot / iconDot wrapper**：`ml-px` → `ml-[2px]`，向右多 1px 與 badge icon 視覺右緣對齊。
- **badge wrapper**：保持 `ml-px`，新增 `mr-px`，讓 icon+overlay 群與 trailing label 之間多 1px 呼吸感，平衡 overlay dot 在 `right:-2` 突出的不對稱。
- 影響 `TabIcon.tsx` 與 `renderInlineTabIcon.tsx` 兩個 surface。

## [1.0.0-alpha.170] - 2026-04-18

### Hotfix: Tab icon 對齊統一 ml-px (#449)

- **alpha.169 對齊翻車**：badge mode 改 `ml-[3px]` 想補償 Phosphor robot glyph 的視覺偏移，方向與幅度都猜錯，結果 robot 變成比 terminal 偏右 2–3px。
- **修正**：badge mode 改回 `ml-px`（alpha.168 值），同時 icon / dot / iconDot 三個 wrapper 也補上 `ml-px`。`TabIcon.tsx` 與 `renderInlineTabIcon.tsx` 兩處同步。
- **效果**：所有 indicator style 的 icon 欄位橫向位置完全脫鉤、落在同一 x。

## [1.0.0-alpha.169] - 2026-04-18

### Fixes / UI: tab/workspace 對齊與對比微調 (#448)

- **Tab icon 對齊**：badge mode wrapper `ml-px` → `ml-[3px]`，補償 Phosphor robot glyph 在 viewbox 中略偏左的視覺偏移，與 terminal icon 對齊；同時為 subagent dots 預留更多左側空間（`TabIcon` / `renderInlineTabIcon`）。
- **Workspace header 按鈕對齊 InlineTab close**：row 加 `pr-1.5`；`+` 與 chevron 都改 12px icon + `p-0.5`，移除 `+` 的 `weight="bold"` 與 chevron 的 `mr-0.5`，並補上 `hover:text-text-primary`，三者視覺與位置一致。
- **Unread 燈號亮度**：`#b91c1c` (red-700) → `#ef4444` (red-500)，比照 running 與 error 級別亮度；影響 `TabIcon` UnreadPip / `TabStatusIndicator` UNREAD_COLOR / `SortableTab` 與 `InlineTab` 角落 pip / `renderInlineTabIcon` UNREAD_PIP。
- **StatusBar host 名稱**：`onClick` → `onDoubleClick`，避免單擊誤開主機設定頁；`status.open_host_hint` zh-TW/en 文案改為「雙擊」。

## [1.0.0-alpha.168] - 2026-04-18

### Refactor: `useTabDisplay` hook 收斂 tab 顯示邏輯 (#447)

- **新 hook `useTabDisplay`**：把原本在 `SortableTab`（top TabBar）與 `InlineTab`（activity bar）各寫一次的 OSC title 解析、agent icon fallback、host offline 判定、agent store 訂閱集中到同一處，兩個 surface 從此共用一份顯示邏輯，未來加 badge / indicator 只需改 hook 一處。
- **InlineTabList 不再計算 title**：原本用跨 host flat session lookup（同 code 不同 host 會標到錯的 session 名），改由 hook 用每個 tab 自己的 `hostId` 做 per-host 查詢 — 順手修掉多 host 命名碰撞的 pre-existing bug。
- **InlineTab `aria-label` 改用 `displayTitle`**：OSC 啟動時螢幕閱讀器讀到的是目前顯示的標題，與視覺一致。
- **砍 `iconMap` prop**：hook 內部從 `ICON_MAP` 解析 pane icon，`TabBar` / `SortableTab` 不再互傳。
- 淨效果：相較 alpha.167，`SortableTab` / `InlineTab` / `InlineTabList` 合計減 130+ 行重複邏輯；新增 155 行 hook 測試涵蓋 label / OSC / per-host 隔離 / host offline / agent 欄位。

## [1.0.0-alpha.167] - 2026-04-18

### UI: TitleBar 搬家 + 視窗按鈕置中 (#446)

- **CollapseButton 移至 TitleBar 左側**：從 ActivityBarWide 的 header-right 與 ActivityBarNarrow 的 divider 下架，改到 TitleBar 左邊、macOS 72px traffic-light 保留區之後的 `sidebar-toggle` 槽（Claude.ai 風格）。
- **Topbar variant 樣式統一**：`p-1 rounded` + 14px icon + `hover:bg-surface-hover`，和 region-toggle / layout-pattern 按鈕同一個視覺 family。
- **TitleBar 高度 30 → 36**：讓 macOS traffic-light 上下置中（y=12 + 6 = 18 ↔ 36/2 = 18）。
- **副作用修正**：按鈕從 ActivityBar 內部（被 DndContext 包的 scrollable 區域）搬出後，先前「展開狀態下按鈕失效」的 bug 一併解決。

## [1.0.0-alpha.166] - 2026-04-18

### Fixes / UI (#445)

- **InlineTab icon 同寬佔位**：`DOT_SLOT` 從 `w-3.5` (14px) 改為 `w-4` (16px)，跟 `TabIcon` 一致。14px icon 放進 16px slot 後四周多 1px 內距，不同 agent / pane icon 寬度統一，tab name 起始 x 對齊。
- **Badge overlay 位置對齊 top bar**：slot 擴大後 `TabStatusIndicator` overlay（`top:-1 right:-2`）相對 icon 的位置跟上方 TabBar 一致。
- **iconDot 結構對齊**：拿掉兩個 slot 之間的 `gap-1`，跟 `TabIcon` 相同。

## [1.0.0-alpha.165] - 2026-04-18

### Fixes / UI (#444)

- **InlineTab 顯示規則對齊 SortableTab**：dot / iconDot 模式內嵌 unread pip，badge 模式改以紅色 overlay；外部 corner pip 加上 `shouldShowGlobalUnreadPip` 判定並對齊上方 TabBar 的位置/大小（`w-2 h-2`、`-top-[4px] -right-[4px]`）。
- **Active tab 移除 accent border**：`border border-accent-muted` → `border border-transparent`，兩態都保留透明 1px 邊框，尺寸不跳。
- **Host tab icon**：`ICON_MAP` 補 `HardDrives`，上方 TabBar 的 host tab 現在能正確渲染硬碟 icon。
- **Settings icon**：`GearSix` → `Sliders`（ActivityBar 寬/窄、WorkspaceContextMenu、SessionPanel、tab-icon-map、pane-labels）。
- **Editor tab icon**（非 diff）：`File` → `TextAlignLeft`，對齊 VSCode 習慣。

## [1.0.0-alpha.164] - 2026-04-18

### Sync P0 — 體質清理 (#432)

- **ConflictBanner**: new UI for resolving per-field sync conflicts. Appears at top of Settings → Sync when pending conflicts exist. Supports expand/collapse, per-row local/remote choice, bulk "keep all local / use all remote", all-or-nothing apply.
- **TitleBar warning icon**: global Phosphor `Warning` icon next to workspace title when there are pending sync conflicts. Tooltip shows count, click deep-links to `/settings/sync`.
- **`/settings/<section>` deep-link**: URL now reflects the active settings section; back/forward navigation and external links (e.g. TitleBar icon) open the correct section.
- **i18n**: full migration of SyncSection — 47 new `settings.sync.*` keys in `en.json` and `zh-TW.json`. Closes #397.
- **Fix #394**: DaemonProvider URL-encodes `clientId` query params; `listHistory` rejects non-positive-integer limits.
- **Fix #395**: `handleExportAll` now honours the `busy` flag to prevent mid-operation export.
- **Fix #396**: `importFromText` enforces 5 MB size + 32 depth limits via typed `ImportError`, surfaced as friendly i18n messages.
- **State**: `useSyncStore` persists `pendingConflicts` / `pendingRemoteBundle` / `pendingConflictsAt` across sessions.

### Round-2 review fixes

- **resolveConflicts push** (R2-A4): `handleResolveConflicts` now pushes the merged bundle back to the daemon provider after a successful resolve, preventing other devices from seeing ghost conflicts on their next pull.
- **pendingRemoteBundle trim** (R1-#2): `setPendingConflicts` keeps only the collections for contributors that actually have conflicts, capping localStorage footprint (`resolveConflicts` never reads the rest).
- **Provider-change race guard** (R2-A6): `handleSyncNow` and `handleFileChange` snapshot `activeProviderId` before their awaits and drop stale results if the user swapped provider mid-flight.
- **TitleBar predicate alignment** (R1-#3): warning icon now matches `SyncSection` banner's full render guard (`provider !== null && remoteBundle && pendingConflictsAt`) so clicking the icon never leads to a banner-less settings page.
- **Settings URL self-heal** (R2-A7): `/settings/<bad>` or `/settings/foo/extra` are replaced with the canonical `/settings/<active>` so back/forward history never preserves garbage URLs.
- **Plural-safe i18n** (R2-A5): 4 count-sensitive keys (`conflict.banner`, `conflict.tooltip`, `conflict.resolved`, `status.conflictsPending`) split into `_one` / `_other` variants via new `pluralKey` helper; English singular stops rendering "1 conflicts".

### Tracked separately (review follow-ups)

- Engine `ResolvedFields` compound-key refactor → #440
- `formatRelativeTime` live tick → #441
- `SyncSection.tsx` split (useSyncActions hook + StatusLine) → #442

### Deferred / tracked separately

- Daemon Pairing UI → gh #421 (Phase P2)
- Cloud Provider → gh #422 (Phase P4)
- Onboarding flow → gh #423 (Phase P6)
- Sync History Dialog → Phase P1
- File Provider → Phase P3
- Content-addressed (Editor) → Phase P5

## [1.0.0-alpha.163] - 2026-04-18

### 介面調整

- **spa**：Activity bar 的 `InlineTab` 視覺對齊上方 `TabBar` —— 移除未定義 token `border-accent-base` 造成的白色左 border（原本 fallback 到 `currentColor`），改用 `border border-accent-muted` / `border border-transparent`；active 文字 `text-white`、inactive 文字 `text-text-muted`，和 `SortableTab` 一致（#443）
- **spa**：`WorkspaceRow` / `HomeRow` 的 `+` 與 chevron 按鈕拿掉硬寫的 `text-text-primary` / `text-text-muted`，改繼承父層顏色，和列內 close X 表現一致（#443）
- **spa**：`InlineTab` 內容左移 6px（`pl-6` → `pl-[18px]`）（#443）
- **spa**：`renderInlineTabIcon` icon 從 12px 改為 14px，對齊上方 `SortableTab` 的 icon 尺寸（`DOT_SLOT` 同步 `w-3 h-3` → `w-3.5 h-3.5`）（#443）

## [1.0.0-alpha.162] - 2026-04-18

### 介面調整

- **spa**：Activity bar collapse 按鈕從 HomeRow 抽出到專屬的上方 header 區，仿 Claude Code desktop 的留白佈局，不再貼著 Home 列（#439）
- **spa**：workspace hover 顯示的 `+` new tab 按鈕改亮改粗（`text-text-primary`、`size=14`、`weight="bold"`），提高可見度（#439）
- **spa**：workspace / home 列的展開收合 chevron 改放到最右側，與 `+` 按鈕同側（#439）
- **spa**：清掉點擊 workspace 列後殘留的瀏覽器預設橘色 focus 外框（header 容器與內部 buttons 加 `focus:outline-none focus-visible:outline-none`）（#439）
- **spa**：Settings → Appearance 的 Tab Position 從 radio list 改為共用的 `SegmentControl`，與 Tab indicator style 欄位風格一致（#439）

## [1.0.0-alpha.161] - 2026-04-18

### 修復

- **spa**：Tab double-click 現在會打開 rename popover（TabBar 與 Activity Bar InlineTab 都生效），複用既有 `openRenameForTab`（#438）
- **spa**：StatusBar `{host}` 由雙擊改名改為單擊開啟 Host 設定頁並選中該 Host（#438）

### 介面調整

- **spa**：StatusBar `{host}` / `{session}` span 移除 `cursor-pointer` —— 仍可互動但不再顯示手型，減輕介面認知負擔（#438）

## [1.0.0-alpha.160] - 2026-04-18

### 功能

- **daemon / spa**：Dev Update 加 SSE build log streaming（#428）—— 新增 `GET /api/dev/update/check/stream`，Settings → Development 按下 Check 後即時顯示 build 的 stdout / stderr / phase 事件，取代原本 3 秒 JSON polling；遲到的 subscriber 拿到完整 replay；並發多個 client 共享同一次 build
- **daemon**：新增 `requiresFullRebuild` 旗標 —— 偵測 `package.json` / `pnpm-lock.yaml` / `electron-builder.yml` / `build/` 變動時，Settings 跳警告 banner 提示需要手動跑 `pnpm run electron:build`（dev update 只抽換 JS bundle）；hash 由 `electron.vite.config.ts` 寫入 `out/.build-info.json` 的 `rebuildHash` 欄位

### 介面調整

- **spa**：新元件 `DevBuildLogPanel` —— 可捲動 `<pre>` 顯示 build log、sticky auto-scroll（使用者本在底部時才跟著捲）、複製完整 log 按鈕；`full_rebuild_hint` 文案指明「執行 daemon 的主機」避免 Air / Mini 情境混淆

### 重構

- **daemon**：`runBuild` 改接受 `*BuildSession` 參數，`CombinedOutput` 換成 pipe-based `streamCmd` 逐行廣播；`snapshotCheck` 拆出唯讀版 `observeCheck` 供終局 SSE 事件使用，避免 build-info 寫失敗時誤觸第二次 build
- **electron**：`streamCheck` 加在 `updater.ts`，SSE 用 fetch + ReadableStream 自解；IPC 每次呼叫配獨立 reply channel，舊 stream 事件不會灌進新 listener
- **spa**：`DevEnvironmentSection` 改用 `streamCheck` IPC；`daemonBase` / `token` 改變時自動重啟 stream 關閉舊 host 殘流

### 測試

- Go 新增 17 個 daemon 測試（BuildSession fanout / 遲到 subscribe / 併發 unsubscribe / handleCheckStream 各狀態 / 終局不觸發第二次 build / client disconnect 釋放 subscription / rebuild-hash 5 case）
- SPA 新增 1 個 `DevEnvironmentSection` test（host 切換重啟 stream）+ 5 個 `DevBuildLogPanel` tests（總計 1876 通過）

### 後續

- Issues #433–#437 追蹤延後項目：build error 脈絡、首次連線 401 文案、`electron/updater.ts` / `internal/module/dev/handler.go` / `DevEnvironmentSection.tsx` 拆分

## [1.0.0-alpha.159] - 2026-04-18

### 介面調整

- **spa**：badge 模式 tab icon + badge 整組右移 1px（`ml-px`），subagent dots 改用 literal `left: -4px` 貼在 box 外緣避開 icon 欄位（#431）；`SubagentDots` 加可選 `left?: number` prop，dot / iconDot 模式沿用原 `calc(50% + offset)` 行為
- **spa**：順手移除 `TabIcon` 在 dot / iconDot branches 傳給 `SubagentDots` 的無效 `isActive` prop

## [1.0.0-alpha.158] - 2026-04-18

### 功能

- **spa**：三選 `tabPosition`（`top` / `left` / `both`）（#427）—— `left` 模式自動展開 active workspace 內嵌 tabs，`top` 隱藏 Activity Bar caret 與 inline `+`，`both` 同時顯示兩個 tab 層（Settings → Appearance 新增三選 radio）
- **spa**：`InlineTab`（left mode）支援 `tabIndicatorStyle`（`icon` / `dot` / `iconDot` / `badge`）—— agent icon、hover close、host-offline `WifiSlash`、locked `Lock` 圖示，與 top-bar tab 達到功能對等；active tab 改用強調左邊框 + 高亮背景，視覺上與 active-workspace 紫色 ring 明確區分
- **spa**：Cross-workspace tab drag 顯示 make-way 動畫 —— 透過 optimistic move 在 target workspace 即時預覽 gap，遵循 dnd-kit multi-list 模式；tab 拖動限制為垂直軸（`x = 0`）
- **spa**：Activity Bar collapse toggle 分拆為三個位置 —— title bar 常駐按鈕、寬欄時 workspace home-row 右側按鈕、窄欄時 divider 右邊緣 hover-reveal overlay

### 介面調整

- **spa**：`+ New tab` 按鈕從各 workspace 底部移至 workspace header 右側（hover-reveal），減少左側欄縱向雜訊
- **spa**：`RegionResize` 可拖拽區域從原本窄縫擴大到 11px、hover 顏色加深，side-panel 與 activity-bar 的拖拽邊界更容易捕捉；視覺縫線維持 1px

### 重構

- **spa**：`SortableTab` 抽離 `ICON_MAP` 為獨立模組 `tab-icon-map.tsx`，left/top tab 共用同一份 icon 註冊
- **spa**：cross-workspace drag orchestration 抽離為 `useCrossWorkspaceDragOver` hook；`useWorkspaceStore.getState()` 在 `removeTabFromWorkspace` 後重新讀取，遵循 PR #392/#419 stale-closure 慣例
- **spa**：`renderInlineTabIcon` 抽離為 `features/workspace/lib/renderInlineTabIcon.tsx`，專門處理 left-mode 12px dot/badge/iconDot 呈現

### 測試

- SPA 合計 1895 tests 通過（+28）：`InlineTab` 行為（indicator styles、host offline、lock、pointerdown dnd-kit 順序 active/inactive 三 case）、`renderInlineTabIcon` 四模式、`useCrossWorkspaceDragOver` optimistic move + pinned/standalone short-circuit、`ActivityBarWide` auto-expand、`WorkspaceRow`/`HomeRow` chevron 條件顯示、`CollapseButton` 三 variant、`RegionResize` 11px hit zone、`useLayoutStore` `both` mode invariant

## [1.0.0-alpha.157] - 2026-04-18

### 功能

- **spa**：Agent 動態標題 + 雙擊 session rename（#424）—— Settings → Terminal 新增「顯示 agent 動態標題」toggle，啟用後 tab label 改顯示 agent 送的 OSC 0/2 title（僅 agent-identified session 生效，shell-only session 保留 session name），hover tooltip 顯示「title - sessionName」；右下 status bar 多一列 OSC title（同樣受 toggle 控制）；左下 status bar 的 host / session name 雙擊進入 session rename popover，anchor 用 `e.currentTarget` 貼合被點元素；`useAgentStore` 新增持久化 `showOscTitle` + ephemeral `oscTitles` per-session map；`useTerminal` 暴露 `onTitle` callback 並綁定 xterm `onTitleChange`

### 介面調整

- **spa**：icon+badge 模式 overlay dot 位置微調 `top:-1px / right:-2px`，與 icon 角落更貼合
- **spa**：未讀視覺改 per-mode 呈現 —— `badge` 模式把 overlay dot 染 unread 紅 (#b91c1c) 取代原本獨立角落 pip；`dot` / `iconDot` 模式在 dot wrapper 右上角疊 5px 小紅 pip；`icon` 模式與尚未收到 agent 事件時保留原本 corner pip 做 fallback
- **spa**：Agent error 狀態改用 `WarningDiamond` duotone 圖示取代燈號 —— `badge` 模式在 overlay 位置繪製；`dot` / `iconDot` 模式整顆 dot 換成 warning diamond
- **spa**：status bar host / session name 加上 `cursor:pointer` + `select-none` + `status.rename_hint` tooltip，視覺上明確可雙擊

### 重構

- **spa**：`TabStatusDot` 改名為 `TabStatusIndicator`（元件現在會渲染 dot 或 WarningDiamond，舊名不再貼合行為）；prop `style` → `mode`；`data-testid` 同步更名 `tab-status-dot` → `tab-status-indicator`
- **spa**：抽 `renderTabIcon` / `UnreadPip` / `shouldShowGlobalUnreadPip` 出 `SortableTab.tsx`，分拆到新檔 `TabIcon.tsx` 與 `tab-icon-helpers.ts`（函式 export 與元件 export 拆開以符合 react-refresh 規則）
- **spa**：`useAgentStore` 新增 `sanitizeOscTitle(raw)` helper，strip ANSI CSI (`\x1b[...m`) 與 C0 控制字元後再存 `oscTitles`，避免第三方 shell 或 agent 嵌 escape 序列時畫面亂碼
- **spa**：`features/workspace/hooks.ts` 提出 `openRenameForTab(tab, anchor?)` helper，context menu 與 StatusBar 雙擊共用同一開啟邏輯，不再複製 rect 計算

### 測試

- SPA 新增 14 個測試：`TabStatusIndicator` unread 紅 tint / error warning-diamond（overlay + replace）3 項；`SortableTab` badge unread 染紅、dot mode pip、error warning-diamond（badge + dot）共 5 項；`useAgentStore` `setShowOscTitle` toggle、`setOscTitle` 的 trim / ANSI strip / C0 strip / equality guard / clearSession / removeHost 共 6 項；`sanitizeOscTitle` 4 分支；`TerminalSection` OSC toggle 1 項；`TerminalView` mock 補 `onTitleChange`；合計 +14，全套 1867 tests 通過

### 文件

- 新增 `docs/superpowers/specs/2026-04-18-cc-statusline-integration-design.md` backlog spec，記錄 CC `statusLine` / hooks 結構化狀態整合的後續設計方向（HTTP POST 路徑、env session 注入、wrapper pattern、跨機 URL 策略），並彙整 hooks 貧乏欄位 vs statusLine 完整 JSON 對照；同步建立 issue #425（InlineTab 對齊 common tab）與 #426（stream mode OSC 擷取）追蹤本版未處理的一致性缺口

## [1.0.0-alpha.156] - 2026-04-17

### 功能

- **spa**：Agent 官方 logo + Tab indicator 樣式 + Claude Code icon 變體（#414）—— `AGENT_ICONS` 改用 `@lobehub/icons-static-svg`（MIT）的官方 Claude Code + Codex logos，透過 `vite-plugin-svgr` 以 React 元件載入並包一層 `{ size, className }` wrapper；Settings → Terminal 新增 Tab indicator style（`icon` / `dot` / `iconDot` / `badge`，預設 `badge`）與 Claude Code icon variant（`bot` / `star`，預設 `bot`，附 live preview button）；`electron.vite.config.ts` 補 `svgr()` plugin，讓 bundled fallback 在 dev server 不可達時也能渲染 SVG

### 介面調整

- **spa**：`@keyframes breathe` 改以 opacity 1 → 0.25 過渡（原本到 0 會短暫消失），避免 subagent dot 在深色主題下看起來像閃爍 bug；overlay dot `right: 0` 貼齊避免 ring 越界到 app 背景
- **spa**：`dot` 模式下在 Claude Code icon 設定加入淡色提示，說明該模式不顯示圖示，此設定需切換到其他模式才會生效

### 重構

- **spa**：`getAgentIcon(agentType, { ccVariant })` 改採 options 物件，`ccVariant` 只對 `cc` 分支有意義，避免 codex 分支忽略參數的 API 不對稱；Codex icon 改用專用 wrapper 元件取代 `as unknown as AgentIconComponent` cast
- **spa**：`DotStyle` 刪除不再使用的 `'inline'` 成員與 `TabStatusDot` fallback 分支；移除 dead CSS vars `--breathe-color` / `--breathe-bg` 與 `SubagentDots` 用不到的 `isActive` prop
- **spa**：清除 `settings.appearance.tab_indicator.*` 六個廢棄 i18n key

### 測試

- SPA 新增 13 個測試：`getAgentIcon` 5 項、`renderTabIcon` 4 分支 + terminated session 邊界、`TerminalSection` SegmentControl 與 CC icon row 互動 3 項；刪除 `TabStatusDot` inline 測試 1 項，合計 +12，全套 1714 tests 通過

## [1.0.0-alpha.155] - 2026-04-17

### 功能

- **spa**：Layout Phase 3 PR D — 跨 workspace tab DnD（#419）—— tab 可跨 workspace 拖放、拖到 Home header 轉 standalone、拖到他 workspace 的 tab-slot 插入指定位置；`WorkspaceRow` / `HomeRow` header 成為 drop target 附 `isOver` ring；custom collision detection（`pointerWithin → rectIntersection → closestCenter`）解決 header 與 tab-slot 重疊；active tab 跟隨搬動、原 ws 變空時 active 自動切目的 ws（關閉 #402）
- **spa**：Spring-load 500ms 自動展開 collapsed header —— 懸停 500ms 於 collapsed workspace / home header 自動展開讓使用者繼續拖入；fire-time 重查 expanded 狀態避免與手動展開競速；`useSpringLoad(delayMs)` 單槽 timer hook（關閉 #403）
- **spa**：Pinned tab 跨 ws drop 禁止 —— pinned tab 只能在所屬 ws 內重排，其他 drop target（他 ws tab-slot / workspace-header / home-header）一律 noop；dragOver 同步短路 spring-load，不對禁止 target 自動展開（關閉 #404）
- **spa**：Cross-ws handler 可從 `ActivityBarProps` 注入 —— 新增 `onMoveTabToWorkspace` / `onMoveTabToStandalone` optional props；預設走 store 直接變更，parent 可 override 做 intercept 或 veto

### 修復

- **spa**：`insertTab` 對不存在 target workspace 的呼叫直接 abort —— 避免並發刪除情境下把 tab 從 source 移除但未插入任何 ws（孤兒 tab）
- **spa**：同 workspace header drop 改為 noop —— 原本經 `insertTab` dedup 會默默改 `activeTabId`；同步消除誤導的 drop ring highlight
- **spa**：Cross-ws handler 改以 `useTabStore.getState()` 讀 activeTabId —— 對齊 PR #392 resize handler 的 stale-closure 修正慣例

### 測試

- SPA 新增 11 個測試（cross-ws 4、pinned guard 3、insertTab orphan/null 3、spring-load 7、droppable header 3），全套 1834 tests 通過

## [1.0.0-alpha.154] - 2026-04-17

### 功能

- **spa**：Sidebar inline tab 視覺對齊 `SortableTab`（#418）—— agent status dot + subagent dots、unread 紅點、離線 host 的 `WifiSlash` icon、locked tab 的 `Lock` icon 並隱藏 Close button；status slot 只在實際有 status/subagent 時渲染，非 tmux tab 不額外縮進（關閉 #401）

## [1.0.0-alpha.153] - 2026-04-17

### 重構

- **spa**：`ActivityBarWide.handleDragEnd` 抽離為 `computeDragEndAction` 純函式 + `dispatchDragEndAction` 分派（#417）—— discriminated action union（noop / reorder-workspaces / reorder-standalone-tabs / reorder-workspace-tabs）讓 Phase 3 cross-workspace drop、spring-load、pinned guard 有清楚擴充點；dispatch switch 加 exhaustiveness never 斷言（關閉 #407）

## [1.0.0-alpha.152] - 2026-04-17

### 修復

- **spa**：Layout Phase 3 hardening（#415）—— `reorderWorkspaceTabs` / `reorderStandaloneTabOrder` 加 stale-subset guard、phantom id 過濾與重複 id dedup；`WorkspaceRow` name button / `InlineTab` 加 pointerdown 守則，避免手震 ≥5px 偷走 click；移除 chevron 無效的 `onMouseDown stopPropagation`（關閉 #405, #406）

## [1.0.0-alpha.151] - 2026-04-17

### 功能

- **spa**：介面設定 + 多欄 New Tab 佈局（#398）—— Settings 新增 Interface section，New Tab 支援 3col/2col/1col 三個 profile，依視窗寬度 ≥1024/≥640/<640 自動切換，可於設定頁拖曳排序欄位順序；首次啟動僅啟用 1col，2col/3col 預填但 disabled，使用者依需求開啟

## [1.0.0-alpha.150] - 2026-04-17

### 功能

- **spa**：Terminal tab icon 依 agent 類型顯示 —— Claude Code 顯示 Lightning、Codex 顯示 Code；未收到 hook event 的 session 維持 `TerminalWindow`/`ChatCircleDots` pane icon，terminated session 保留 `SmileySad` tombstone（#413）

## [1.0.0-alpha.149] - 2026-04-17

### 介面調整

- **spa**：Status bar 主機名/session 名、Title bar 標題與右側按鈕統一採用 `text-text-secondary`，與 Activity bar icon 顏色一致（#400）
- **spa**：Settings `SettingItem` 外層改為 `items-start`，Sync 頁 Modules 區塊 label 不再被右側多行 checkbox 撐開垂直置中（#411）

## [1.0.0-alpha.148] - 2026-04-17

### 功能

- **spa**：Sync Now 接線 — `sync-actions.ts` 編排 `SyncEngine.pull → push` 並處理 ok / conflicts / error 三種結果
- **spa**：Import apply 接線 — 匯入檔案透過 one-shot provider 走 `engine.pull` 三方合併，不再停留於 stub
- **spa**：`useSyncStore.syncHostId` — Daemon provider 的目標 host（持久化）
- **spa**：Settings → Sync 新增 Sync Host dropdown（Daemon only），Sync Now / Import 顯示 busy / success / warn / error 狀態行

### 修復

- **spa**：衝突分支正確推進 `lastSyncedBundle` — 對 engine 已套用的非衝突 contributor 前進 baseline，避免下次 sync 以過時 ancestor 重比（issue #388）

### 測試

- SPA 新增 9 個 sync wiring 測試（syncNow / applyImport / partialBaseline 各 variant），全套 1638 tests 通過

## [1.0.0-alpha.147] - 2026-04-17

### 功能

- **spa**：Activity bar 寬窄雙模式 — 窄版維持現況（44px icon），寬版顯示 icon + workspace 名稱（預設 240px），可拖右邊界 resize（120-600 clamp）
- **spa**：Activity bar 底部 `CollapseButton` 切換寬窄；`tabPosition='left'`（Phase 2 上線後）時鎖定為寬版
- **spa**：`useLayoutStore` 新增 `activityBarWidth` / `tabPosition` / `activityBarWideSize` / `workspaceExpanded` state，持久化到 `purdex-layout` 並透過既有 `syncManager` 跨視窗同步
- **spa**：`reconcileWorkspaceExpanded` GC — workspace 刪除時清除 stale expanded 狀態，保留 `home` 保留鍵
- **spa**：`healLayoutInvariant` rehydrate self-heal — 自動修復 `{narrow, left}` 無效組合
- **spa**：Resize handle 新增 `onResizeEnd` 支援，拖曳期間使用 ephemeral state，釋放時才 commit 避免 localStorage 風暴
- **spa**：i18n — `nav.collapse_activity_bar` / `nav.expand_activity_bar` / `nav.collapse_locked_tooltip`

### 重構

- **spa**：`ActivityBar` 拆為協調者 + `ActivityBarNarrow`（既有行為）+ `ActivityBarWide`（新）
- **spa**：共用 `ActivityBarProps` type 抽到 `activity-bar-props.ts`

### 測試

- SPA 新增 54 個 layout 測試（useLayoutStore、CollapseButton、ActivityBar coordinator、Narrow/Wide 變體）

## [1.0.0-alpha.146] - 2026-04-17

### 功能

- **spa**：SyncEngine — pluggable module contributor registry + SyncBundle serialize/deserialize + 三方比對衝突偵測
- **spa**：7 個 SyncContributor — workspaces、hosts（排除 auth token）、preferences、layout、quick-commands、i18n、notification-settings
- **spa**：3 個 SyncProvider — Manual（匯出/匯入）、Daemon（REST push/pull）、File（iCloud/Syncthing 同步資料夾）
- **daemon**：Sync module — SQLite 儲存（groups、canonical bundle、history、pairing codes）+ 9 個 HTTP endpoints
- **spa**：Sync Settings UI — provider 選擇器、module checkboxes、匯出/匯入、sync status

### 測試

- SPA 新增 182 個 sync 測試（15 test files），全套 1626 tests 無回歸
- Go daemon 新增 5 個 sync store 測試

## [1.0.0-alpha.145] - 2026-04-16

### 修復

- **spa**：補上 `settings.section.editor_buffers` i18n 翻譯（zh-TW「編輯器暫存」、en "Editor Buffers"）

## [1.0.0-alpha.144] - 2026-04-16

### 功能

- **spa**：Tiptap v3 WYSIWYG — Markdown 檔案支援 raw/WYSIWYG 雙模式切換（lazy load）
- **spa**：Monaco Diff View — 未存檔變更的 side-by-side diff 比較
- **spa**：Image/PDF Preview Pane — 新增 `image-preview` / `pdf-preview` pane kind
- **spa**：外部變更偵測 — tab focus 時比對 stat，clean buffer 靜默 reload
- **electron**：LocalBackend — IPC fs handler（home dir sandbox + realpath）+ LocalBackend class
- **spa**：BufferListSection — Settings 頁面 in-app buffer 管理

### 修復

- **spa**：修正 Tiptap v3 `setContent` API（v2 三參數 → v3 options object）
- **spa**：PdfPreviewPane iframe 加 `sandbox=""` 防止 XSS
- **spa**：`contentMatches` 補上 image/pdf preview daemon hostId 檢查
- **spa**：`markSaved` 新增 stat 參數，save 後更新 `lastStat` 避免無謂 re-read
- **electron**：`validatePath` 改用 `realpath()` 防止 symlink bypass
- **spa**：TiptapEditor 加 `internalUpdateRef` 防止 content sync thrashing
- **spa**：BufferListSection `handleDelete` 加 error handling

### 測試

- 新增 image/pdf preview `contentMatches` 測試（8 cases）
- 新增 `LocalBackend` 測試（11 cases）
- 新增 `markSaved` stat 更新測試（2 cases）

（#380）

## [1.0.0-alpha.143] - 2026-04-16

### 功能

- **spa**：`WorkspaceIconPicker` 和 `WorkspaceDeleteDialog` 加入 Escape 鍵關閉支援，與其他 overlay 元件行為一致（#195, #378）

## [1.0.0-alpha.142] - 2026-04-16

### 修復

- **spa**：`useUISettingsStore` 新增 `onRehydrateStorage`，啟動時自動將 WebGL renderer 下的 `keepAliveCount` clamp 至上限 6，避免既有高值設定超出 GPU context 限制（#188, #377）

## [1.0.0-alpha.141] - 2026-04-16

### 修復

- **spa**：`useRelayWsManager` useEffect cleanup 後，in-flight `fetchWsTicket` promise 不再建立遊離 WS 連線；加入 `cancelled` flag 阻止 cleanup 後的 `.then()` 執行（#173, #376）

## [1.0.0-alpha.140] - 2026-04-16

### 雜務

- **daemon**：`cmd/pdx/quick.go` 更名為 `pairing_init.go`，名稱與實際職責一致
- **spa**：刪除 dead i18n keys（`hosts.pairing_connecting`、`hosts.saving`）
- **spa**：AddHostDialog IP/port/token 輸入加 `.trim()` 防止空白造成 URL 錯誤
- **spa**：AddHostDialog 加 `aria-labelledby` 改善無障礙
- **docs**：修正 plan doc stale ref（`internal/core/base58` → `internal/pairing/base58`）及 spec L75 文字（#166, #375）

## [1.0.0-alpha.139] - 2026-04-16

### 修復

- **spa**：Workspace 自動命名改用 `nextWorkspaceName()` 尋找最小未使用的 `Workspace N`，避免刪除 workspace 後新建時產生重複名稱（#199, #374）

## [1.0.0-alpha.138] - 2026-04-16

### 修復

- **spa**：PaneHeader swap menu 加入 `useClickOutside` 和 Escape 鍵關閉機制，行為與 TabContextMenu 一致（#249, #373）

## [1.0.0-alpha.137] - 2026-04-16

### 修復

- **spa**：`checkHealth` 在 HTTP 200 但 `res.json()` 拋出 SyntaxError 時，不再錯誤歸類為 `refused`（L2），改為正確回傳 `connected`；新增 inner try-catch 包住 JSON parse，失敗時 early return 不進入 Phase 2（#372）

## [1.0.0-alpha.136] - 2026-04-16

### 重構

- **spa**：`useLayoutStore` 新增 `partialize` 明確持久化欄位 + `reconcileViews()` 啟動時驗證 views 有效性、清除 stale ID、首次安裝自動填入 defaults；移除 `main.tsx` 的 ad-hoc `hasAnyView` 邏輯（#370）

## [1.0.0-alpha.135] - 2026-04-16

### 修復

- **dev**：設定頁檢查更新觸發的自動編譯，改為先執行 `pnpm install --frozen-lockfile` 與 icon 生成，再跑 `electron-vite build`；同時改善步驟化錯誤訊息，避免缺依賴時反覆編譯失敗（#369）

## [1.0.0-alpha.134] - 2026-04-15

### 測試

- **daemon**：為 files module 新增 Go 測試（9 個案例），覆蓋 handleList 所有程式路徑（#367）

## [1.0.0-alpha.133] - 2026-04-15

### 重構

- **spa**：將 `SidebarRegion` 型別從 `types/tab.ts` 搬移至 `types/layout.ts`，釐清 layout 系統與 tab 系統的型別邊界（#366）

## [1.0.0-alpha.132] - 2026-04-15

### 修復

- **electron/spa**：Home 模式下封鎖 Cmd+Alt+Up/Down 工作區切換；新增 Cmd+Alt+0 跳至 Home（#364）

## [1.0.0-alpha.131] - 2026-04-15

### 新增

- **spa**：Editor Module Plan A — Monaco 基礎編輯器 + FS 抽象層（#359）
  - `FsBackend` 介面 + `InAppBackend`（in-memory）+ backend registry
  - `file-opener-registry` 解耦檔案來源與開啟行為
  - `module-registry` `pane` → `panes` 遷移，支援多 pane kind
  - `editor` PaneContent kind + pane utilities 更新
  - `useEditorStore` buffer store（runtime，不 persist）
  - `MonacoWrapper`（⌘S + cursor tracking）+ `EditorToolbar` + `EditorStatusBar`
  - `EditorPane` 主元件：載入 → buffer → 編輯 → 存檔全流程
  - New Tab 入口：「新增檔案」+「新增 Markdown」

## [1.0.0-alpha.130] - 2026-04-15

### 修復

- **spa**：Icon picker grid 從固定 8 欄改為 ResizeObserver 動態計算欄數，填滿容器寬度（#358）

## [1.0.0-alpha.129] - 2026-04-15

### 修復

- **electron**：`electron:build` 前置執行 `generate-icon-data.mjs`，確保 bundled SPA 包含 icon weight JSON（#357）
- **spa**：移除 Workspace Settings 頁面的重複 Style weight switcher，改由 IconPicker 統一管理（#357）
- **agent**：上傳檔案注入從 `send-keys` 改為 `paste-buffer -p`（bracketed paste），讓 CC 自動偵測圖片路徑並 chip 化為 `[Image #N]`；使用具名 buffer 避免並發競態（#357）

## [1.0.0-alpha.128] - 2026-04-14

### 修復

- **electron**：啟動預設載入 bundled SPA，避免 dev server 模組解析問題導致 crash 白屏；mini-browser 同步修正（#356）
- **chore**：移除殘留的 `spa/pnpm-lock.yaml`（pnpm workspace 只需 root lockfile）

## [1.0.0-alpha.127] - 2026-04-14

### 修復

- **spa**：standalone tab 聚焦時 Home badge 正確顯示其他 standalone tabs 的未讀數，排除聚焦 tab 本身的計數（#253, #355）

## [1.0.0-alpha.126] - 2026-04-14

### 修復

- **spa**：browser tab 插入位置修正 — workspace 排序現在正確使用 `insertTab`（PR #233 regression），並改為插入在右側最近 browser tab 之後（#353）

## [1.0.0-alpha.125] - 2026-04-14

### 重構

- **spa**：拆分 `store.test.ts`（611 行）為 `store-workspace.test.ts`（194 行）+ `store-tabs.test.ts`（431 行），51 tests 完整保留（#254, #354）

## [1.0.0-alpha.124] - 2026-04-14

### 修復

- **daemon**：`pdx setup` 在 daemon 不可達時加入 local fallback，直接呼叫 provider hook 安裝邏輯，不再強制要求 daemon 運行（#255, #352）

## [1.0.0-alpha.123] - 2026-04-14

### 修復

- **spa**：bump `useAgentStore` persist version 2→3，確保舊用戶的 `tabIndicatorStyle` 重設為新預設 `replace`（#268, #351）

## [1.0.0-alpha.122] - 2026-04-14

### 修復

- **spa**：TitleBar 標題改用 `max-w-[calc(100%-26rem)]` 取代固定 `px-20` padding，防止標題在極端情況與右側 layout 按鈕重疊（#267, #350）

## [1.0.0-alpha.121] - 2026-04-14

### 修復

- **spa**：`ModuleConfigSection` boolean toggle 改用 `ToggleSwitch` 元件，補齊 `role="switch"`、`aria-checked`、`type="button"` 等 a11y 屬性；text/number 欄位加上 `htmlFor`/`id` label 關聯（#258, #348）

## [1.0.0-alpha.120] - 2026-04-14

### 修復

- **spa**：`FileTreeWorkspaceView` 在 `workspaceId` 為 undefined（standalone tab 模式）時，不再渲染無法使用的 projectPath 表單，改為顯示「請先選擇 Workspace」提示（#257, #346）

## [1.0.0-alpha.119] - 2026-04-14

### 功能

- **daemon**：Terminal relay WS 加入 ping/pong keep-alive（30s interval, 10s pong timeout），防止 proxy/firewall 靜默斷開閒置連線（#160, #345）

## [1.0.0-alpha.118] - 2026-04-14

### 修復

- **spa**：Terminal WS `canReconnect` gate 加入 host 存在性檢查，防止 host 刪除後 ghost reconnect（#159, #344）

## [1.0.0-alpha.117] - 2026-04-14

### 效能

- **daemon**：`GetTmuxInstance()` 和 `getTmuxVersion()` 加入 3s exec timeout，防止 tmux server 異常時 `/api/info` handler 阻塞（#154, #343）

## [1.0.0-alpha.116] - 2026-04-14

### 功能

- **spa**：SessionPanel / SessionSection 的 host header 支援點擊收合/展開，多 host 時以 CaretDown/CaretRight 指示狀態，active host 不可收合（#144, #342）

## [1.0.0-alpha.115] - 2026-04-14

### 功能

- **daemon+spa**：Upload 暫存目錄路徑可在 UploadSection 中編輯，透過 daemon config 持久化（#340）

### 修復

- **daemon**：upload handler 的 `uploadDir` 讀寫加 mutex 保護，防止 config 變更時的 data race（#340）

## [1.0.0-alpha.114] - 2026-04-14

### 重構

- **spa**：提取 register-modules 匿名 pane wrapper 為命名元件（#338）

## [1.0.0-alpha.113] - 2026-04-14

### 修復

- **spa**：Workspace name input 加入 `maxLength=64` 防止超長名稱（#337）

## [1.0.0-alpha.112] - 2026-04-14

### 修復

- **spa**：ActivityBar badge 數字超過 99 時截斷為 `99+`（#336）

## [1.0.0-alpha.111] - 2026-04-14

### 功能

- **spa**：RenamePopover 新增 client-side session name 驗證（#335）
  - 即時格式檢查，鏡像 daemon 的 `^[a-zA-Z0-9_-]+$` 正則
  - 抽取 `isValidSessionName()` utility 至 `lib/session-name.ts`
  - Legacy session name 不觸發 popover 打開即顯示錯誤
  - 新增 i18n key（en + zh-TW）

## [1.0.0-alpha.110] - 2026-04-13

### 重構

- **daemon**：Probe Chain 三層探測架構取代舊 CC Detector（#334）
  - Liveness 層：process name + child process + content fallback，統一 CC/Codex 偵測
  - Readiness 層：ReadinessChecker interface，CC/Codex 各自實作狀態辨識
  - Activity 層：CapturePaneContent hash diff 偵測畫面變化，解決黃燈卡住問題
  - 刪除 `cc.Detector`、`cc.CCDetector` interface、`codex.detector`
  - Stream orchestrator 改用 `IsAliveFor` + `CheckReadiness` 組合

## [1.0.0-alpha.109] - 2026-04-13

### 功能

- **spa**：Icon 系統重構 — React.lazy + 1,445 chunks 改為 SVG path data 架構（#333）
  - Build-time 腳本從 `@phosphor-icons/core` 提取 SVG path，產生 6 個 per-weight 靜態 JSON
  - `icon-path-cache` 按需 fetch + 記憶體快取 + concurrent dedup + 失敗重試
  - `WorkspaceIcon` 同步 SVG 渲染，消除 Suspense 閃爍
  - `WorkspaceIconPicker` 改用 Fuse.js 模糊搜尋（支援 tags/categories）+ TanStack Virtual 虛擬捲動
  - Picker 新增 weight toggle UI，支援 6 種 Phosphor weight 即時預覽
  - `IconWeight` 從 3 種擴充為全部 6 種（bold/regular/thin/light/fill/duotone）
  - Build 產出：JS 檔案 1,446 → 1，dist 9.0MB → 6.1MB，main bundle 453KB → 448KB gz

## [1.0.0-alpha.108] - 2026-04-13

### 測試

- **spa**：新增 SortableTab 測試 — onPointerDown focus prevention、data-tab-id attribute、onSelect/onClose 互動（#211）

## [1.0.0-alpha.107] - 2026-04-13

### 功能

- **spa**：Workspace tooltip 顯示 unread 數量與 agent 狀態，與 aria-label 一致（#228）

### 修正

- **spa**：修正 active workspace 的 aria-label 錯誤包含 agent status（#228 review 發現）

## [1.0.0-alpha.106] - 2026-04-13

### 功能

- **spa**：RenamePopover 新增垂直 viewport clamping — 下方空間不足時翻轉到 anchor 上方，上下皆不足時 clamp 到頂部（#212）

## [1.0.0-alpha.105] - 2026-04-13

### 功能

- **daemon + spa**：Session list 即時同步 — SPA 新增 `useSessionWatch()` ref-counted polling hook，daemon `handleList` 新增 1s TTL debounce cache（#128）

## [1.0.0-alpha.104] - 2026-04-13

### 測試

- **spa**：補充 HandoffButton `agentStatus='error'` 和 `'waiting'` 測試案例（#125）

## [1.0.0-alpha.103] - 2026-04-13

### 修正

- **daemon**：`Stop()` 取消進行中的 build goroutine，防止 daemon restart 時產生並行 build（#99）

## [1.0.0-alpha.102] - 2026-04-13

### 功能

- **daemon**：dev auto-build 新增 5 分鐘逾時，防止 electron-vite 卡住導致 building 狀態永久鎖定（#97）

## [1.0.0-alpha.101] - 2026-04-13

### 重構

- **spa**：拆分 `useTabStore.test.ts` (517→343 行)，獨立 `terminated` 和 `migration` 測試檔案（#213）

## [1.0.0-alpha.100] - 2026-04-13

### 修正

- **electron**：merge window list 在新視窗 SPA 未載入時顯示 'Purdex' fallback 而非空白按鈕（#220）

## [1.0.0-alpha.99] - 2026-04-13

### 重構

- **spa**：建立 `closeTab()` helper 統一 locked guard 和 `destroyBrowserViewIfNeeded` 呼叫，修��� WorkspaceSettingsPage BrowserView 洩漏（#217）

## [1.0.0-alpha.98] - 2026-04-13

### 測試

- **electron**：新增 `keybindings.ts` 19 個單元測試 + vitest 測試基礎設施（#83）

## [1.0.0-alpha.97] - 2026-04-13

### 修正

- **daemon**：`handleDownload` 改為 buffer tar.gz 後才送出，Walk 錯誤時回傳 HTTP 500 而非產出損壞的 tar（#82）

## [1.0.0-alpha.96] - 2026-04-13

### 修正

- **daemon**：dev update 改用 config `repo_root` 欄位取代 `os.Getwd()`，daemon 從非 repo 目錄啟動時不再失效（#79）

## [1.0.0-alpha.95] - 2026-04-13

### 修正

- **LocaleEditor / ThemeEditor**：編輯 custom entry 時就地更新，不再每次 Save 建立重複項目（#74）

## [1.0.0-alpha.94] - 2026-04-13

### 變更

- **App icon**：符合 Apple HIG 標準（留白 100px、圓角 185.4px、陰影 Y12 blur28 @1024）
- **Per-arch build**：`scripts/build-electron.mjs` 按 arch 切換 icon 打包
- **i18n**：補齊 `settings.section.modules` 翻譯
- **ActivityBar**：Home 按鈕使用 Purdex 透明 logo

## [1.0.0-alpha.93] - 2026-04-12

### 修正

- **App icon**：改用白色圓角背景版，ActivityBar 保留透明版（`logo-transparent.png`）
- **i18n**：補齊 `settings.section.modules` 翻譯（Modules / 模組）
- **Legacy hooks**：清除舊 tbox hook 殘留，不做向下相容

## [1.0.0-alpha.92] - 2026-04-12

- chore: rename cleanup + Purdex logo (#315)

### 變更

- **Go 內部符號**：`tboxPath`/`makeTboxEntry`/`isTboxCommand` 等全部更名為 `pdxPath`/`makePdxEntry`/`isPdxCommand`
- **App 圖示**：PWA icons、macOS .icns、favicon 全部替換為 Purdex logo
- **ActivityBar**：Home 按鈕使用 Purdex logo 取代 SquaresFour icon
- **Maskable icon**：加入紫色背景 + 70% safe zone padding
- **型別安全**：新增 `SplitLayout` 型別 + `isSplit` type guard，移除 `PaneLayoutRenderer` 的 unsafe `as` cast
- **測試**：新增 `isGrid4` 6 個單元測試

## [1.0.0-alpha.91] - 2026-04-12

- refactor: brand rename from tmux-box to Purdex (#308, #309, #310, #311, #312)

### 變更

- **專案更名**：tmux-box → Purdex，CLI binary tbox → pdx
- **Go module**：`github.com/wake/tmux-box` → `github.com/wake/purdex`
- **Config 路徑**：`~/.config/tbox/` → `~/.config/pdx/`
- **環境變數**：`TBOX_TOKEN` → `PDX_TOKEN`、`TBOX_DEV_UPDATE` → `PDX_DEV_UPDATE`
- **tmux session channel**：`tbox_sess_evt` → `purdex_sess_evt`
- **Electron**：appId `dev.wake.purdex`、productName `Purdex`

## [1.0.0-alpha.90] - 2026-04-12

- feat: daemon background mode + crash log + reconnect error clear (#307)

### 新增

- **`tbox start/stop/status` 子命令**：daemon 可背景啟動，使用 `flock` PID file 管理生命週期，`stop` 以 SIGTERM 優雅關閉（30 秒 timeout + SIGKILL fallback）
- **Crash log**：`runServe` 加 panic recover defer，寫入 `~/.config/tbox/logs/crash-YYYYMMDD-HHMMSS.log`，含 secret redaction（Authorization header、purdex\_/tbox\_ token、cfg.Token）
- **Logs 子頁**：per-host Logs sub-page 顯示 Daemon Log（`/api/logs/daemon`）+ Crash Logs（`/api/logs/crash`），含手動 refresh + offline 狀態

### 修正

- **Reconnect 後 testResult 殘留**：`OverviewSection` 加 transition-aware `useEffect`，只在 status 從非 connected 轉為 connected 時清除 stale 錯誤訊息，避免 `manualRetry()` 循環清掉成功 pill

### 追蹤

- #305 — safeGo helper for cross-goroutine panic recovery
- #306 — HTTP recover middleware

## [1.0.0-alpha.89] - 2026-04-12

- fix(electron): restore renderer focus when backgrounding BrowserView (#301)

### 修正

- **切回 Terminal tab 後 terminal 無法 auto-focus**：使用者點過 Browser tab 的 `WebContentsView` 內容後，OS 鍵盤 focus 會留在那個 webContents；切回 Terminal tab 時 `BrowserPane` unmount → `closeBrowserView` → `BrowserViewManager.deactivate()` 只把 view 移到 off-screen，沒把 focus 交回主視窗 renderer，導致 `TerminalView` 在 visible-effect 中呼叫的 `termRef.current.focus()` 形同無效（DOM element focus 撈不到鍵盤輸入），使用者必須手動點一下 terminal 才能恢復輸入。修法在 `deactivate()` 移 off-screen 後呼叫 `entry.window.webContents.focus()` 把鍵盤 focus 交回 host renderer；以 `entry.window.isFocused()` 守衛避免 multi-window 場景搶奪其他 window 的 focus。後續在 #302/#303/#304 追蹤 destroy/discard 路徑、pop-out 流程、反向 activate focus 等延伸問題

## [1.0.0-alpha.88] - 2026-04-12

- revert: undo ineffective TitleBar cursor-pointer attempts (#296, #297) (#299)

### 回退

- **TitleBar 右側按鈕 pointer cursor 問題無法用 CSS 解決**：`#296`（把 `-webkit-app-region: drag` 移到 flex spacer）與 `#297`（加 `relative z-10` + `cursor-pointer!` important）都已用 Electron DevTools 驗證對 macOS OS 游標毫無影響 — computed cursor 已經是 `pointer !important`，問題出在 Electron 上游（[electron/electron#5723](https://github.com/electron/electron/issues/5723)、[#21632](https://github.com/electron/electron/issues/21632)）：`titleBarStyle: 'hiddenInset'` 下 NSWindow 仍保留頂部 ~38px 的 title bar 區域並攔截 Chromium 的 cursor update，但 click event 正常。將 `spa/src/components/TitleBar.tsx` 回退到 `#296` 之前的狀態，避免保留無效的 dead code；真正的修復（結構性繞開，仿 VSCode 把按鈕放到 tracking zone 以外）改由 #300 追蹤，標 `pending` 暫擱

## [1.0.0-alpha.87] - 2026-04-12

- fix(electron): restore Toggle Developer Tools in View menu (#298)

### 修正

- **DevTools 快捷鍵失效**：`electron/keybindings.ts:buildMenuTemplate` 的 View menu 只收錄 `byCategory.get('View')` 的自訂項目，完全沒有 `role: 'toggleDevTools'`，自訂 menu 一旦 `Menu.setApplicationMenu` 取代預設 menu，Electron 內建的 `Cmd+Option+I` 就一併遺失。在 View submenu 尾端補上 separator + `{ role: 'toggleDevTools' }`，Electron 自動綁回預設 accelerator，`Cmd+Option+I` 與 View → Toggle Developer Tools 都可打開 DevTools

## [1.0.0-alpha.86] - 2026-04-12

- fix(spa): force cursor pointer on TitleBar buttons (#297)

### 修正

- **TitleBar 按鈕 cursor pointer 強制生效**：#296 只把 `-webkit-app-region: drag` 從容器移到 spacer，但 absolute `inset-0 pointer-events-none` 的 title 層在 paint order 上仍高於未定位的 buttons（CSS positioned siblings 不論 DOM 順序皆覆於 non-positioned siblings 上），Electron/Chromium 的 cursor fall-through 在此情境下不穩。將 buttons 容器升入 positioned stacking layer（`relative z-10`）讓它確實畫在 title 之上、重新加回 `WebkitAppRegion: 'no-drag'` 雙保險、每顆 button 改用 Tailwind v4 的 `cursor-pointer!`（產生 `!important`）硬覆寫任何競爭的 cursor 規則，disabled 態對應 `disabled:cursor-default!`

## [1.0.0-alpha.85] - 2026-04-12

- fix(spa): restore cursor pointer on TitleBar buttons (#296)

### 修正

- **TitleBar 右側按鈕無 pointer cursor**：原本 `spa/src/components/TitleBar.tsx` 最外層容器帶 `-webkit-app-region: drag`，Chromium 對 drag region 的所有子孫強制使用預設游標，即便按鈕 container 已設 `no-drag` 也無法覆寫 `cursor-pointer`。改為最外層不帶 drag、另外插入一個 `flex-1 self-stretch` spacer 承擔 `WebkitAppRegion: 'drag'`，讓右側按鈕脫離 drag region 祖先鏈；視窗拖曳行為由 spacer 提供，置中標題仍以 `absolute inset-0 pointer-events-none` 跨滿整條 bar

## [1.0.0-alpha.84] - 2026-04-12

- fix(session): guard handleCreate with mutex against same-name race (#61) (#294)

### 修正

- **#61 handleCreate TOCTOU race**：`internal/module/session/handler.go:handleCreate` 原本從 `HasSession` 到 `SetMeta` 完全無鎖，兩個同名 POST 可以同時通過 duplicate check 然後各自呼叫 `NewSession`，在 FakeExecutor 上重現為 `sessionOrder` 重複 entry（確定性），在真實 tmux 上會讓第二次呼叫失敗並回傳 500 而非 409。新增 `SessionModule.createMu sync.Mutex`，`handleCreate` 於輸入驗證後 `Lock + defer Unlock`，涵蓋整段 `HasSession → NewSession → ListSessions → SetMeta` critical section。Watcher goroutines 不取此鎖，可能看到中間狀態但下一輪會自動修正（已追蹤為 #295）
- **test infra — `:memory:` connection pool pinning**：`internal/store/meta.go:OpenMeta(":memory:")` 新增 `db.SetMaxOpenConns(1)`。Go `database/sql` 的 pool 可能對同一 DSN 開多條連線，而每條 `:memory:` 連線各自是一個獨立的空 DB，先前讓並發測試隨機打到 empty DB 出現 `no such table: session_meta`。只對 `:memory:` 路徑生效，production 使用 `cfg.DataDir/meta.db` 不受影響
- **新增並發回歸測試**：`TestHandlerCreateSessionConcurrentSameName` 以 close-on-start barrier 釋放 N=100 個 goroutine 同時 POST 相同 session name，斷言恰好 1 個 201、99 個 409、`ListSessions()` 長度 == 1；於 `-race -count=20` 下穩定通過

## [1.0.0-alpha.83] - 2026-04-12

- fix: rollback in-memory config on writeConfig failure (#28) (#293)

### 修正

- **#28 atomic config update**：`internal/core/config_handler.go` 的 `handlePutConfig` 原本先更新記憶體 `c.Cfg`、再寫檔；若 `config.WriteFile` 失敗則回 500 但記憶體已變，造成執行中的 daemon 使用新設定、重啟後讀回舊設定的不一致。改在 mutation 前 `snapshot := *c.Cfg`，寫檔失敗時 `*c.Cfg = snapshot`（透過指標寫回，保留其他 goroutine 持有的 `c.Cfg` 指標身分），整段都在 `CfgMu.Lock` 範圍內；`NotifyConfigChange` 仍只在成功分支觸發
- **新增 rollback 回歸測試**：`TestPutConfigRollsBackOnWriteFailure` 將 `CfgPath` 指向「父路徑是一般檔案」的位置，觸發 `MkdirAll` ENOTDIR（跨平台可靠、無需 chmod cleanup），驗證 500 + 錯誤訊息含 "not a directory"、Stream/Detect/Terminal 全數 rollback、callback 未觸發、`c.Cfg` 指標身分未變、rollback 後再發一次正常 PUT 仍可成功
- **新增 invariant 註解**：snapshot 與 mutation block 各有一段註解明確要求所有 mutation 必須整個欄位指派，禁止 `append` 或 map in-place 寫入（shallow snapshot rollback 正確性的前提）

## [1.0.0-alpha.82] - 2026-04-12

- fix: lock CfgMu when reading sizing mode in HandleTerminalWS (#26) (#292)

### 修正

- **#26 race fix**：`internal/module/session/service.go` 的 `HandleTerminalWS` 讀取 `m.core.Cfg.Terminal.GetSizingMode()` 時未持 `CfgMu.RLock`，與 `handlePutConfig` 在 `CfgMu.Lock` 下寫入相同欄位產生 data race。改採 snapshot pattern：read lock 內取值後立即解鎖，仿 `internal/module/stream/handler.go:177-182` 既有 pattern
- **新增 race 回歸測試**：`TestHandleTerminalWS_NoConfigRace`（50 reader × 20 iterations + 1 writer），於 `go test -race` 下驗證；修復前命中 DATA RACE，修復後通過。Test cleanup 用 `sync.Once + t.Cleanup` 確保 panic 時 writer goroutine 不 leak

## [1.0.0-alpha.81] - 2026-04-11

- refactor: App.tsx 拆分 — 提取 hooks + 具名 callback (#282)
- fix: SubagentDots 燈號殘留 (5 root cause + 3 輪 review follow-up) (#283)

### 重構

- **App.tsx 409 → 286 行**：提取 `GlobalUndoToast` 為獨立元件、`useElectronIpc` hook（收納 4 個 IPC effect）、`useWorkspaceWindowActions` hook（workspace tear-off/merge）
- **`openSingletonAndSelect`**：在 `useTabWorkspaceActions` 新增 helper 統一 4 處 singleton tab 開啟模式，支援可選 `wsId` 參數
- **Inline lambda 全面具名化**：JSX 不再包含業務邏輯，所有 handler 提為 `useCallback`
- Closes #202, #219, #225, #237, #243, #261, #281

### 修正

- **#231**：`onWorkspaceReceived` catch 範圍縮窄，僅捕 `JSON.parse` 錯誤避免靜默吞掉 store mutation 錯誤
- **`openWsSettings` cross-workspace**：修正右鍵非 active workspace 的 settings 時 tab 被插入錯誤 workspace
- **Bug 0 (主因)**：移除 `Subagents` 的 `omitempty` tag，最後一筆 `SubagentStop` 永遠送 `subagents:[]`，前端不再卡住
- **Bug 0b**：新增 `RenameSessionAtomic(old, new, doRename)` API，把 tmux + DB + in-memory rename 包進單一 lock，修復 rename 後 hook 用新名查空 map 的問題
- **Bug 1**：新增 `useAgentStore.clearSession(hostId, code)` action，session-closed 時集中清理
- **Bug 2**：`checkAliveAll` orphan 分支改用 `tmux.HasSession()` 二次確認，防止 `ListSessions` 暫時性失敗時誤刪
- **Bug 3**：`SubagentStart` guard 改用 `events.Get + DeriveStatus(StatusClear)` 持久化 DB state，修復 daemon restart + compact `SessionStart` 邊界
- **rename rollback**：`renameSessionAtomic` 改為 DB-first，tmux 失敗時 best-effort 回滾 DB，確保三方一致

## [1.0.0-alpha.80] - 2026-04-10

fix: SubagentDots 相位同步 + terminal reconnect 自動恢復 (#279)

### 修正

- **SubagentDots 動畫相位同步**：`useMemo` dependency 改為 `[clamped]` + key 強制 remount，修正新 subagent dot 與既有 dot 相位不同步的雜亂閃爍
- **Terminal reconnect gate 持續重試**：`canReconnect()` gate 回 false 時改為固定間隔輪詢（不累積 backoff），host 恢復後自動重連
- **connectWithTicket 殭屍 WS 防護**：await getTicket 後加 `closed` guard，防止 unmount 後建立多餘 WebSocket

## [1.0.0-alpha.79] - 2026-04-10

fix: topbar cursor pointer + host 子分頁切換保留 (#276)

### 修正

- **TitleBar 按鈕 cursor pointer**：明確加 `cursor-pointer` class 修正 Electron drag region 覆蓋 cursor 樣式的問題
- **Host 子分頁切換保留**：展開不同 host 時保留當前選中的 subPage，不再 hardcode reset 到 overview

## [1.0.0-alpha.78] - 2026-04-10

Quick Commands + Host Agents + tmux 精確匹配修正 (#269)

### 新增

- **Quick Commands Module**：可插拔指令快捷系統 — module registry `commands` extension point、QuickCommandStore（global + per-host persist）、`useCommands()` hook、QuickCommandMenu 下拉選單
- **POST /api/sessions/{code}/send-keys**：透過 `SendKeysRaw` 送指令到 tmux session
- **GET /api/agents/detect**：偵測 host 上 claude/codex CLI 安裝狀態（path + version）
- **Host > Agents 子頁面**：顯示各 agent CLI 的安裝狀態
- **Codex Hooks**：HOOK_MODULES 加入 CODEX_HOOKS，Hooks 頁面顯示 tmux/CC/Codex 三區

### 修正

- **tmux 精確匹配**：RenameSession / KillSession / send-keys target 加 `=` 前綴避免 prefix matching 誤判
- **handleRename 重名 409**：rename 前檢查目標名稱是否已存在
- **CC getLastTrigger**：加 `agent_type` 過濾，避免 codex event 被算進 CC

### 移除

- **Settings > Agent 區塊**：hooks 統一走 Host > Hooks 管理

## [1.0.0-alpha.77] - 2026-04-10

Sidebar / Panel View Management (#266)

### 新增

- **Region 管理 UI**：每個 sidebar/panel 可自行管理啟用哪些 view、拖曳排序
- **三個管理入口**：⚙ 按鈕（pinned header）、+ 按鈕（collapsed bar）、右鍵 context menu
- **RegionManager 元件**：替換 region 內容的管理畫面，@dnd-kit drag-to-reorder
- **RegionContextMenu 元件**：右鍵 checkbox 清單快速開關 view
- **View scope 三層模型**：ViewDefinition.scope 支援 `'system' | 'workspace' | 'tab'`
- **Layout Store 新 actions**：`addView`、`removeView`、`reorderViews`

### 改善

- **toggleVisibility 記憶狀態**：TitleBar toggle 隱藏後恢復時記住之前是 pinned 或 collapsed
- **Region 空狀態**：空 region 仍渲染管理入口（不再完全消失）
- **TitleBar**：始終顯示全部 4 個 region toggle 按鈕

### 移除

- `ViewDefinition.defaultRegion`（view 不再綁定特定 region）
- `getViewsByRegion()` 查詢函式（改為 `getAllViews()`）
- `WorkspaceSidebarState` 型別（未被使用）

## [1.0.0-alpha.76] - 2026-04-10

### 修正

- **Topbar 標題置中**：改用 absolute 定位，以完整視窗寬度置中，不受右側按鈕影響 (#262)
- **全域 cursor: pointer**：全域 CSS 規則讓所有 button、[role="tab"]、[role="button"] 預設顯示 pointer cursor (#263)
- **Workspace icon 拖曳範圍限制**：restrictToVertical modifier 增加 Y 軸邊界計算，限制拖曳在列表範圍內 (#264)

### 變更

- **Tab Indicator Style 設定隱藏**：從 Settings UI 移除選項，預設改為 replace，保留 store 架構

## [1.0.0-alpha.75] - 2026-04-10

### 修正

- **Session 建立重名 409**：`handleCreate` 建 session 前先 `HasSession` 檢查，回 409 Conflict + 明確訊息；`HasSession` 改用 `=` 前綴精確匹配避免 tmux prefix matching 誤判 (#260)
- **createRequest 加 Mode 欄位**：前端傳的 mode 不再被 JSON decoder 靜默忽略，新建 session 正確套用 terminal/stream (#260)
- **Workspace icon 拖曳排序恢復**：恢復在 status pill refactor 中遺失的 `@dnd-kit` DnD（`SortableWorkspaceButton`、`reorderWorkspaces` store action）；加防禦性 guard 防止 stale orderedIds 丟失 workspace (#260)
- **Tab bar + 按鈕位置**：移除 `normalTabsRef` 的 `flex-1`，+ 按鈕緊鄰最後一個 tab (#260)
- **前端錯誤訊息改善**：NewSessionDialog 顯示 response body 而非僅 HTTP status code (#260)

## [1.0.0-alpha.74] - 2026-04-10

### 修正

- **Topbar region toggle**：從展開/收合改為完全顯示/隱藏，新增 `toggleVisibility` action (#259)
- **Workspace module settings**：WorkspaceSettingsPage 加入 ModuleConfigSection，workspace 範圍模組設定現在可見 (#259)

## [1.0.0-alpha.73] - 2026-04-10

Agent Module Provider Pattern 重構 (#247)

### 新增

- **AgentProvider 介面**：capability-based composition（HookInstaller、HistoryProvider、StreamCapable），支援多 agent 擴充
- **Agent Registry**：thread-safe provider 註冊/查詢，hook event 路由依 agent_type 分派
- **CC Provider**：從散落的 cc/detect 模組整合為 `internal/agent/cc/`（detector、operator、history、hooks、status derivation）
- **Codex Provider**：新增 `internal/agent/codex/`（status derivation、process detection、hook installer）
- **NormalizedEvent**：後端推導 status/model/subagents，前端零 per-agent 邏輯
- **Per-agent hook 管理**：`GET/POST /api/hooks/{agent}/status|setup` 參數化 API
- **Agent icons**：`spa/src/lib/agent-icons.ts` icon + name mapping
- **AgentSection UI**：Settings 頁面顯示每個 agent 的 hook 安裝狀態與操作按鈕

### 改善

- **CLI `--agent` flag**：`tbox hook` 和 `tbox setup` 必須指定 agent type
- **useAgentStore 簡化**：移除前端 `deriveStatus`，store 只接收後端 pre-derived 狀態
- **Frontend agent-agnostic**：新增 agent 只需後端加 provider + 前端加一行 icon mapping

### 移除

- `internal/module/cc/`（整個目錄）— 合併至 `internal/agent/cc/`
- `internal/detect/`（整個目錄）— 合併至 `internal/agent/cc/`
- `internal/module/agent/cc_hooks.go` — 移至 `internal/agent/cc/hooks.go`
- 前端 `deriveStatus` 函式、`AgentHookEvent` 型別、`clearSubagentsForHost` action

## [1.0.0-alpha.72] - 2026-04-10

Sidebar/Panel/Pane 修正 + Module Config 系統 (#245)

### 新增

- **Module Config 系統**（#244）：Module 透過 registry 宣告 workspace/global 層級設定，Workspace 提供泛用 `moduleConfig` 儲存，Settings 頁面自動產生表單
- **Files Module 拆分**：workspace view（以 projectPath 為根）+ session view（placeholder，待 daemon cwd API）
- **Pane swap 功能**：PaneHeader 新增交換按鈕，可在同 tab 的不同 pane 間交換內容
- **Tab mergeToTab**：Tab 右鍵選單新增「加入 Tab 成為 pane」功能
- **TitleBar Region Toggle**：4 個 sidebar/panel 切換按鈕，僅在 region 有 view 時顯示
- **Global Module Config Store**：`useModuleConfigStore` 支援全域 module 設定持久化

### 改善

- **PaneSplitter 視覺**：hover 加寬、顏色加深、hit area 擴大
- **PaneHeader 視覺**：按鈕加大、邊框加強
- **Grid-4 水平聯動**：四宮格水平 splitter 同步 resize
- **Pane detach 位置**：彈出的 tab 插入到來源 tab 的下一位（而非尾端）
- **SidebarRegion props**：正確傳遞 region/workspaceId/hostId 給 view component
- **insertTab afterTabId**：workspace store 支援指定位置插入 tab

## [1.0.0-alpha.71] - 2026-04-10

Home 按鈕未讀指示修正 + standalone tab 關閉 scope 修正

### 修正

- **Home badge 未讀數**：Home 按鈕 badge 改用 `useWorkspaceIndicators` 計算未讀數，而非顯示 standalone tab 總數
- **Home status dot**：Home 按鈕新增 status dot indicator（running/waiting/error），與 workspace 按鈕一致
- **standalone tab 關閉 scope**：關閉 standalone tab 時，nextTab 候選範圍改為只包含其他 standalone tabs，避免跳到 workspace tab
- **standaloneTabIds memoize**：`useMemo` 穩定 `standaloneTabIds` 陣列引用，避免每次 render 重算 indicator

## [1.0.0-alpha.70] - 2026-04-09

Workspace 狀態指示器迭代

### 變更

- **Status dot**：workspace icon 狀態指示器從 3px 左側 pill 改為 5px 圓點，與 TabStatusDot 風格一致
- **Active 隱藏**：active workspace 不顯示狀態圓點（狀態已可在 tab bar 看到）
- **Aria-label 強化**：workspace 按鈕的 aria-label 現在包含 agent 狀態（running/waiting/error）
- **測試補強**：新增 4 個 status dot 測試（顯示/隱藏/waiting 靜態/aria-label）

## [1.0.0-alpha.69] - 2026-04-09

Module Layout Foundation (Plan 1+2) + Review 修正

### 新功能

- **Module Registry**：統一 pane + view 註冊系統（`module-registry.ts`），取代舊 `pane-registry.ts`
- **Layout Store**：4-region sidebar/panel 狀態管理（`useLayoutStore`），持久化到 `purdex-layout`
- **TitleBar**：Electron 標題列元件，含 traffic light safe zone + layout pattern 按鈕 placeholder
- **SidebarRegion**：可折疊/展開的 sidebar 容器，支援 view 切換 + 拖曳調整寬度
- **RegionResize**：拖曳調整 region 寬度的把手元件
- **App 佈局重構**：統一 TabBar 位置 + 4 SidebarRegion 整合

### 修正（Review）

- **RegionResize stale closure**：drag 時 `onResize` callback 使用 `useRef` 保持最新引用
- **syncManager 註冊遺漏**：`useLayoutStore` 現在正確註冊 syncManager，跨 tab/視窗同步
- **ViewDefinition icon 型別**：`icon` 從 `string` 改為 React component type，collapsed bar 渲染 Phosphor Icon
- **activeViewId fallback**：展開 region 時若 activeViewId 未設定，自動 fallback 到第一個 view
- **移除 `mode: 'default'`**：移除從未使用的 mode 值，RegionState 只保留 `'pinned' | 'collapsed'`
- **`side` → `resizeEdge`**：prop 更名為更清晰的語義
- **TitleBar layout 按鈕加 disabled**：Plan 3 前 placeholder 按鈕標記為 disabled
- **移除 `--app-region` 死碼**：TitleBar 移除無效的 CSS custom property
- **PaneLayoutRenderer 空 children 防護**：split layout children 為空時顯示 fallback
- **SidebarRegion 收合按鈕**：展開狀態新增 collapse button

## [1.0.0-alpha.68] - 2026-04-09

Workspace icon indicators — unread badge + status pill (PR #226)

## [1.0.0-alpha.67] - 2026-04-09

Tab 拖曳 / Rename CORS / 通知 GC / Tab 溢出 / 上傳階段修復 (PR #224)

### 修正

- **Active tab 無法拖曳**：dnd-kit 檢查 `nativeEvent.defaultPrevented` 會靜默中止拖曳；調整 `handlePointerDown` 順序，先呼叫 dnd-kit handler 再 `preventDefault()`
- **Rename session "Failed to fetch"**：CORS middleware 缺少 `PATCH` method，瀏覽器 preflight 被拒
- **通知點擊無反應**：Electron `Notification` JS wrapper 在 `show()` 後被 GC 回收（C++ 層不使用 `SelfKeepAlive`），用 `Set<Notification>` 保持強引用
- **Tab 溢出整個 app**：Electron title bar tab 容器缺少寬度約束；加入 `flex-1 min-w-0` 並讓 `normalTabsRef` 使用 `flex-1` + `min-content` 讓 tab 先縮減再捲動

### 新功能

- **上傳「輸入中…」階段**：圖片上傳流程新增 `typing` 狀態，完整流程為 `uploading → typing(1.5s) → done(3s) → dismiss`

## [1.0.0-alpha.66] - 2026-04-09

Browser UX 修復 — mini window theme + tab shortcuts (PR #223)

### 修正

- **Mini window toolbar 不可見**：獨立瀏覽器視窗現在正確初始化 theme（ThemeInjector + useThemeStore hook），toolbar 可見
- **Mini window Cmd+W**：獨立視窗現在可用 Cmd+W 關閉

### 新功能

- **Tab shortcut handler registry**：不同 tab type 可註冊各自的快捷鍵 handler，useShortcuts 作為 dispatcher
- **Browser 導航快捷鍵**：Cmd+[/] (back/forward)、Cmd+←/→ (macOS)、Cmd+R (reload)、Cmd+L (focus URL)、Cmd+P (print)
- **Print IPC**：新增 browser-view:print channel，支援 Cmd+P 列印

## [1.0.0-alpha.65] - 2026-04-09

Home tab 殘留修復 + workspace icon tooltip (PR #221)

### 修正

- **Home tab 殘留**：切回 Home 時若無 standalone tabs，`activeTabId` 現在正確清除為 null，不再殘留前一個 workspace 的 tab
- **Workspace icon tooltip**：ActivityBar workspace icon hover 即時顯示名稱（CSS tooltip），取代原生 title 延遲；改用 `aria-label` 避免雙重 tooltip

## [1.0.0-alpha.64] - 2026-04-09

新增 ⌘⇧H 快捷鍵開啟 Host 管理面板 (#178)

### 新功能

- **⌘⇧H 開啟 Hosts 面板** — 以 singleton tab 方式開啟 Host 管理頁面，與 ⌘, (Settings)、⌘Y (History) 操作模式一致

## [1.0.0-alpha.63] - 2026-04-09

Workspace 增強 — Ctrl+Tab、通知導航、workspace tear-off/merge (PR #218)

### 新功能

- **Ctrl+Tab / Ctrl+Shift+Tab** — 切換 tab 的替代快捷鍵（macOS 部分鍵盤設定可能衝突）
- **通知點擊切 workspace** — 點擊通知自動切換到含有該 tab 的 workspace（或回到 Home）
- **Workspace tear-off** — 右鍵 workspace context menu「獨立到新視窗」，整個 workspace 搬到新 Electron 視窗
- **Workspace merge** — 右鍵「合併到視窗」，將 workspace 合併到另一個已開啟的視窗
- **importWorkspace store action** — 跨視窗 workspace 傳輸，含 ID 去重

### 修正

- merge 視窗清單過濾當前視窗（避免 self-merge）
- IPC 呼叫成功後才清理 store（防止失敗時資料遺失）
- merge 目標消失時正確 throw（不再靜默失敗）
- workspace 接收端驗證 activeTabId 存在性
- tear-off/merge 後全域 activeTabId 與 workspace activeTabId 同步
- spa:ready listener 在視窗關閉時清除（防止洩漏）
- 禁止 tear-off 空 workspace（0 tabs guard）
- getWindows() IPC 加 .catch() 防止永久 Loading

## [1.0.0-alpha.62] - 2026-04-08

Workspace activeTabId 同步修正

### 修正

- **快捷鍵 tab 切換同步 workspace** — switch-tab-*、prev/next-tab、switch-workspace 快捷鍵現在正確同步 `ws.activeTabId`，切回 workspace 時恢復最後瀏覽的 tab
- **冗餘寫入優化** — `activateTab` helper 在值未改變時跳過 `setWorkspaceActiveTab`

## [1.0.0-alpha.61] - 2026-04-08

Close-tab workspace scoping 重構 + ActivityBar 間距 (PR #216)

### 重構

- **`closeTabInWorkspace` composite action** — 取代 hook 層 post-close 補丁，在 workspace store 一次完成 recordClose → removeFromWorkspace → closeTab → workspace-scoped active tab 選取（visitHistory 優先 → adjacent fallback）
- **`closeTab` 簡化** — 移除全域 tabOrder auto-select，只負責刪除 tab + 清理 visitHistory
- **5 個 caller 統一遷移** — useShortcuts、hooks.ts、TerminatedPane、WorkspaceSettingsPage、host-lifecycle
- **`destroyBrowserViewIfNeeded` 共用 helper** — 修正 useShortcuts close-tab 缺少 browser view cleanup

### 修正

- **`ws.activeTabId` 同步** — close-tab 後正確同步 workspace 的 activeTabId（修 PR #208 review issue）
- **`ws.activeTabId` 覆寫** — 關閉非 active tab 不再錯誤覆寫 workspace activeTabId
- **host-lifecycle undo 還原 workspace** — cascade delete undo 現在同時還原 tab 的 workspace 歸屬
- **host-lifecycle skipHistory** — cascade delete 不再污染「最近關閉」記錄

### 改善

- **ActivityBar 按鈕尺寸** — 32px → 30px + 容器 px-px，減少側邊欄擠壓

### 測試

- 1045 tests pass / lint clean / build OK

## [1.0.0-alpha.60] - 2026-04-08

URL history dropdown 對齊修正

### 修正

- **dropdown 位置** — URL 歷史下拉選單左邊界對齊 input 左側，不再包含 Globe icon 的空間

## [1.0.0-alpha.59] - 2026-04-08

Tab UX 改善 (PR #209)

### 新功能

- **Rename Session** — tab 右鍵選單新增 Rename Session，popover 出現在 tab 正下方 inline 編輯，API 失敗時顯示錯誤訊息
- **URL 歷史下拉** — new tab 頁面 URL 欄位帶出歷史紀錄，輸入時 auto-filter，鍵盤 ↑↓ 選擇，最多 100 筆持久化
- **Session 鍵盤導航** — Tab 鍵進入 session list，↑↓/jk 移動，Enter 選擇
- **瀏覽紀錄回退** — 關閉 tab 時回到上一個瀏覽的 tab（visitHistory stack），而非相鄰 tab

### 改善

- **New Tab 頁面** — browser URL 欄位移到最上方並自動 focus，移除 Memory Monitor 區段
- **Focus 保持** — 點擊已 active 的 tab 不搶走 content 區域的 focus

### 測試

- 1047 tests pass / lint clean / build OK

## [1.0.0-alpha.58] - 2026-04-08

Tab 操作 workspace 隔離 (PR #208)

### 修正

- **close-tab 跨 workspace 防護** — cmd+w 只能關閉當前 workspace 可見的 tab，空 workspace 時不會誤刪其他 workspace 的 tab
- **post-close scoping** — 關閉後若 closeTab 自動選了其他 workspace 的 tab，重設為當前 workspace 內的 tab 或 null

### 測試

- 1012 tests pass / lint clean
- 新增跨 workspace close-tab 和 reopen-closed-tab 測試

## [1.0.0-alpha.57] - 2026-04-08

Workspace UI 微調 (PR #207)

### 改善

- **Context menu 精簡** — 只保留 Settings，移除 rename/color/icon/delete（已在設定頁可用）
- **Settings sidebar 左邊距** — active 指示線不再緊貼 ActivityBar
- **Icon picker ring 修正** — 選中 icon 的紫色邊框不再被裁切

### 清理

- **移除 workspace 顏色系統** — 刪除 WorkspaceColorPicker、WorkspaceRenameDialog、WorkspaceChip、workspaceColorStyle、WORKSPACE_COLORS 等死碼（7 檔 302 行）
- Workspace interface 移除 `color` 欄位
- 清理 App.tsx ~80 行 dialog state

### 測試

- 1009 tests pass / lint clean

## [1.0.0-alpha.56] - 2026-04-08

Phase 11 — Workspace UI 改善 (PR #201)

### 新功能

- **Workspace 設定頁** — 前台式單頁設定（名稱編輯、Phosphor Icon picker、icon weight toggle、刪除）
- **Phosphor Icons Picker** — 8 分類精選 + 搜尋完整 1512 icon 庫，lazy loading per-icon chunk
- **Icon Weight** — bold / duotone / fill 三種風格切換
- **Empty workspace state** — 切換到無 tab workspace 時顯示空白引導頁
- **WorkspaceContextMenu** — 新增 Settings 項目
- **Vibrant color palette** — S=55-80% L=55-65% 取代舊 S=36% 色板（12 色）

### 改善

- **ActivityBar** — 系統配色（白前景/深背景）、active 紫色背景 + ring-purple-400
- **WorkspaceChip 移除** — tab bar 不再顯示 workspace 標題，上下文完全由 ActivityBar 提供
- **Tab recall fix** — 切換 workspace 時正確回到上次瀏覽的 tab（getState 取代 stale closure）
- **useRouteSync** — workspace-settings 路由補 setActiveWorkspace + insertTab

### Review 修正

- WorkspaceIcon rules-of-hooks — useMemo 移到 early return 前避免條件式 hook 呼叫
- WorkspaceIcon ErrorBoundary — icon import 失敗時 fallback 至文字而非 crash 整個 app
- WorkspaceSettingsPage delete — 濾除 settings tab、記錄 history、sync activeTab
- Context menu 點 Settings 後正確關閉
- 搜尋去重：curated icons 跨 category 重複時不再產生 React key 警告
- 修正無效 Phosphor icon 名稱（Tv → Television, Brackets → BracketsCurly）
- workspaceColorStyle JSDoc 修正

### 測試

- 997 tests pass / lint clean

## [1.0.0-alpha.55] - 2026-04-07

Browser Tab 強化 (PR #200)

### 新功能

- **Browser tab toolbar** — 導航按鈕（← → ↻/✕）、可編輯 URL 欄位、⋯ 更多選單
- **Mini browser 獨立視窗** — Shift+click 連結彈出獨立視窗，共用同一套 BrowserToolbar
- **Terminal 連結統一處理** — click 開新 browser tab、shift+click 開 mini browser（SPA fallback 為 window.open）
- **WebContentsView preload 注入** — 攔截頁面連結點擊，回報 shiftKey 給 main process
- **MiniWindowManager** — 管理 mini browser BrowserWindow 生命週期
- **browser-view-ipc.ts** — 集中管理所有 browser-view IPC handler
- **useBrowserViewState hook** — 訂閱 Electron state-update（URL、title、canGoBack、isLoading）
- **useBrowserViewResize hook** — 共用 ResizeObserver 邏輯
- **link-handler factory** — createLinkHandler 根據 platform + shiftKey 分派
- **URL 正規化** — normalizeUrl utility（自動補 https://、scheme 白名單）
- **Browser tab close → destroy** — 主動關閉走 destroy 路徑，tab 切換走 background

### 測試

- 1011 tests pass / lint clean

## [1.0.0-alpha.54] - 2026-04-07

Phase 10 — Workspace 強化 (PR #189, #190, #191)

### 新功能

- **Workspace 全自由制** — 移除預設 workspace，支援 0 workspace 模式，`activeWorkspaceId` 可為 null
- **Feature module 架構** — workspace 相關程式碼搬遷至 `features/workspace/`（store、hooks、components、lib）
- **insertTab store action** — 原子化操作 + singleton dedup（跨 workspace 移除重複 tab）
- **getVisibleTabIds 共用函式** — 純函式，含 Home mode 支援
- **insertTab 收斂** — 所有 `addTabToWorkspace + setWorkspaceActiveTab` 模式統一為 `insertTab`
- **WorkspaceDeleteDialog** — 刪除確認 UI + tab 勾選清單
- **右鍵選單 + Chip** — WorkspaceContextMenu + Titlebar WorkspaceChip
- **重新命名/顏色/圖示設定** — RenameDialog、ColorPicker、IconPicker
- **Electron 快捷鍵** — ⌘⌥1-9 位置切換 + ⌘⌥↑/↓ 循環切換
- **MigrateTabsDialog** — 首個 workspace 建立時詢問遷移既有 tab
- **Standalone Tabs Home 入口** — ActivityBar 頂部 Home 按鈕

### 修正

- deleteWs 後同步 activeTabId 到新 workspace
- Home 按鈕高亮條件修正
- MigrateTabsDialog Skip 後自動切回 Home
- prev-workspace 從 Home 出發跳到最後一個 workspace

### 測試

- 981 tests pass / lint clean

## [1.0.0-alpha.53] - 2026-04-07

Phase 7.4 — Daemon 品質改善 (PR #196, #133, #130, #131, #121, #134)

### 修正

- **upload delete TOCTOU (#133)** — 移除 `os.Stat` check-then-act，直接 `os.Remove` + `IsNotExist` 判斷
- **send-keys 失敗清理 (#130)** — `SendKeysRaw` 失敗時 `os.Remove(destPath)` 清理孤立檔案
- **dedup filename TOCTOU (#131)** — `deduplicateFilename` 改為 `createDedupFile` 使用 `O_CREATE|O_EXCL` 原子佔檔名
- **settings.json atomic write (#121)** — `mergeHooks` 改用 tmp + `os.Rename` 原子寫入

### 效能

- **session watcher debounce (#134)** — `broadcastSessions()` 加 500ms debounce，防止 wait-for + ticker 重複廣播

### 測試

- 新增 6 個 Go 測試（upload delete 404/success、send-keys fail cleanup、atomic write、debounce、debounce expiry）

## [1.0.0-alpha.52] - 2026-04-07

Phase 7.3 — Refactor 拆檔 (PR #192, #163, #138, #185, #182)

### 重構

- **deleteHostCascade 提取 (#163)** — 從 OverviewSection.tsx 提取 cascade delete + undo 邏輯至 `lib/host-lifecycle.ts`，同時修正原本遺漏的 `models` snapshot/restore
- **form-fields 提取 (#138, #185)** — Section / Field / EditableField / TokenField 提取至 `hosts/form-fields.tsx`，OverviewSection 655→280 行
- **cc_hooks 拆分 (#182)** — handler.go CC hook 邏輯移至 `cc_hooks.go`，與 session/hooks.go 結構對稱，handler.go 227→87 行

### 關閉（無需修復）

- **#184** — deriveStatus 已是獨立 exported pure function，提取無實質改善

### 測試

- Tests: 913 → **914**（新增 agentModels undo 測試）

## [1.0.0-alpha.51] - 2026-04-07

Phase 7.2 — UI 元件修正 (PR #187, #139, #179)

### 修正

- **HostSidebar auto-expand (#139)** — selectedHostId 變更時（如 host 刪除 fallback）自動展開新 host，用 derived state（`||`）取代 useEffect
- **WebGL context loss handler (#179 L1)** — `onContextLoss` → dispose addon → fallback DomRenderer → re-fit，防止 terminal 切離再切回時縮小
- **TabContent visibility:hidden (#179 L2)** — 取代 `left:-9999em` off-screen hack，語意更正確
- **keepAliveCount WebGL cap (#179 L3)** — WebGL 上限 6、DOM 上限 10，DOM→WebGL 切換時 auto-clamp，settings 顯示 hint

### 關閉（無需修復）

- **#140** — 已在 commit `e8cfe7ff` 修復（empty draft 跳過驗證）
- **#157** — storage key 切換早於欄位新增，舊資料已遺棄
- **#176** — 架構上場景不成立（inactive tab off-screen）

### 測試

- Tests: 906 → **912**（新增 HostSidebar auto-expand、TabContent visibility、TerminalSection WebGL cap、WebGL context loss 行為測試）

## [1.0.0-alpha.50] - 2026-04-06

Phase 7.1 — Lint + Agent Store 修正 (PR #183, #175, #105, #92, #93, #94, #126, #124, #110, #169)

### 修正

- **SubagentStop events 保護 (#126)** — SubagentStart/Stop 不再覆寫 events map，只更新 activeSubagents
- **Error 狀態白名單 (#124)** — 只有 UserPromptSubmit/SessionStart/Stop 可清除 error，非白名單事件完全跳過（events + status 都不更新）
- **PermissionRequest i18n (#105)** — 通知 body 改用 `{{tool}}` 參數
- **Notification body (#110)** — permission_prompt/elicitation_dialog 顯示特定 body
- **SubagentDots 同步 (#169)** — negative CSS animation-delay 同步呼吸動畫相位
- **Rename tbox_ → purdex_ (#175)** — user-facing 字串 + `tbox_version` → `purdex_version` API 欄位
- **setState-in-useEffect (#92)** — useReducer、render-time state、direct DOM、ref deps
- **useCallback deps (#93)** — 修復遺漏 deps + justified eslint-disable
- **Lint cleanup (#94)** — fast-refresh、explicit-any、globals-reassign
- **TerminalView retrying** — SM cycle 完成後 `Promise.resolve().finally()` 清除 spinner
- **manualRetry 型別** — `() => void` → `() => Promise<void> | void`

### 改善

- Lint: 31 errors/warnings → **0**
- Tests: 904 → **906**（新增 error guard + events 一致性測試）

## [1.0.0-alpha.49] - 2026-04-06

Phase 6 — Hooks 統一架構 (PR #181, #150, #109, #108, #103, #142, #127)

### 新功能

- **Daemon API 統一** — tmux 和 CC hooks 統一為 `/api/hooks/{module}/status` + `/setup` 路由模式
- **HookModule 介面** — 新增模組化架構，新 hook module 只需加一個 config 物件
- **HookModuleCard 元件** — 通用 card 元件，含安裝/移除按鈕、事件狀態、loading spinner
- **Agent Hooks UI 完成** — CC hooks 從 stub 升級為完整的安裝/移除/狀態/錯誤 UI
- **Model Name 持久化 (#127)** — `models` map 防止 model name 被後續事件覆寫
- **觸發時間顯示 (#142)** — `getLastTrigger` API + 相對時間顯示（剛剛 / Nm ago / Nh ago）

### 修正

- **StatusBar reactivity** — model badge 改用 reactive store selector，確保 hook event 到達後即時更新
- **getLastTrigger 解耦** — 從 lib 層移除 store 依賴，改為純函式 + `useMemo` 穩定引用
- **setup() unmount guard** — `mountedRef` 防止 unmount 後 setState
- **exec.Command timeout** — CC hook setup 加入 30s context timeout
- **Response agent_type 移除** — hook status/setup API 回應不再包含多餘的 `agent_type` 欄位
- **i18n placeholder** — 修正 `{n}` → `{{n}}` 格式

### 改善

- **useModuleHook** — 共用 data-fetching hook，管理 loading/error/status 生命週期
- **死路徑清除** — 移除 `useHookStatus`、`App.tsx` init fetch、`hooksInstalled` 欄位、舊 API 函式
- **測試覆蓋** — 新增 `HookModuleCard.test.tsx`（13 tests）、`useModuleHook.test.ts`（9 tests）、`hooks_test.go`（5 tests）、setup handler tests（4 tests）、reactive tests

### 關閉 Issues

- #150, #109, #108, #103, #142, #127, #114, #64

## [1.0.0-alpha.48] - 2026-04-06

api.ts 舊 API 層缺 auth — 遷移至 hostFetch 統一認證 (PR #180, #177)

### 修正

- **API 層統一認證** — 將 `api.ts` 全部 9 個函式遷移至 `host-api.ts`，改用 `hostFetch` 自動帶 Bearer token，解決 daemon 設 token 後所有 API 呼叫 401 的問題
- **Raw fetch 修正** — `App.tsx`、`useHookStatus.ts` 的 3 處 raw fetch 改用 `fetchAgentHookStatus` / `setupAgentHook`
- **Electron updater auth** — `checkUpdate` / `applyUpdate` 新增 `token?` 參數，透過 IPC 傳遞

### 改善

- **Store 簽名簡化** — `useSessionStore.fetchHost(hostId, base)` → `fetchHost(hostId)`、`useConfigStore.fetch/update(base)` → `fetch/update(hostId)`，消除 caller 傳錯 base URL 的可能
- **Dead code 清理** — 移除 `electron/main.ts` 啟動時的 background update check（`dev:update-available` 無 listener、main process 無法取得 auth token）
- **測試補充** — 新增 `host-api.test.ts`（21 個測試），覆蓋所有遷移函式含 auth header 驗證

### 刪除

- `spa/src/lib/api.ts` — 舊 API 層，已完全由 `host-api.ts` 取代

## [1.0.0-alpha.47] - 2026-04-06

Phase 5b — WS Ticket 統一 + Auth Error UI (PR #168)

### 新功能

- **Negotiation-First 狀態機** — `checkHealth` 升級為兩階段（GET /api/health + POST /api/ws-ticket），同時驗證 daemon reachability 與 token auth
- **WS Ticket 統一** — terminal、stream、host-events 三條 WS 全面使用 ticket auth
- **Auth Error 狀態** — `HostRuntime.status` 新增 `'auth-error'`，狀態機偵測 401/503 後不重試
- **Auth Error UI** — HostSidebar 鎖頭圖示（animate-pulse）、StatusBar 可點擊導航至設定頁、OverviewSection 紅色 banner + Token 自動重試
- **Health Mode 消費（#167）** — AddHostDialog 根據 daemon mode 自動導流（pairing → 配對碼、pending/normal → Token）
- **Per-host diff 更新** — 修改單一 host 的 IP/Port 只重建該 host 連線，不影響其他 host
- **connectHostEvents lazy mode** — WS 不立即連線，等待狀態機 negotiation 完成後由 `reconnectWithTicket` 啟動
- **connectTerminal 雙函式設計** — sync `connect()` + async `connectWithTicket()`，既有同步呼叫端不受影響

### 安全性

- **移除 `?token=` query param fallback** — 消除 token 出現在 URL/log/瀏覽器歷史的風險
- **Token-less host 偵測** — SPA 無 token 時回傳 `auth-error`（daemon pairing mode 除外），避免 WS 被 daemon 401 拒絕後的死循環
- **PairingGuard 503 偵測** — daemon 在 pairing mode 時 ws-ticket 回 503，狀態機正確判定為 auth-error

### 修正

- **狀態機 + Auth 死循環** — health endpoint 不驗 auth → 誤判 connected → WS 靜默失敗。兩階段 negotiation 解決
- **新 host token 錯時靜默失敗** — 狀態機不自動啟動 → 灰色圓圈無回饋。lazy mode + 立即 `sm.trigger()` 解決
- **`startBackground` auth-error guard** — L1 背景重試遇 auth-error 正確停止，不再無限循環
- **`ws.close()`/`send()`/`resize()` 安全性** — async getTicket 期間 ws 未初始化時加 optional chaining
- **Relay 雙重 stream WS** — `pendingFetches` Set 防止快速 reconnect 建立重複 WS
- **`reconnect()`/`reconnectWithTicket()` double-trigger** — `ws.onclose = null` 後再 close，防止 onclose handler 重複觸發

### 關聯 Issues

- #148 pt.2 — Terminal/Stream WS auth（統一用 ticket）
- #148 pt.3 — WS 401 auth error 提示
- #167 — health mode SPA 消費

## [1.0.0-alpha.46] - 2026-04-05

Phase 5a — 配對系統 + Token 認證 (PR #164)

### 新功能

- **Quick 模式** — `tbox serve --quick`：13 碼 Base58 配對碼（IP+Port+Secret 編碼），PairingGuard 攔截非配對 API，verify → setupSecret → setup 三步完成
- **一般模式** — `tbox serve`（無 token）：daemon 產生 `purdex_` runtime token 印到 terminal，client 用 `POST /api/token/auth` 確認後持久化
- **PairingState 狀態機** — pairing/pending/normal 三態，thread-safe（atomic.Int32）
- **SetupSecretStore** — 128-bit one-time secret，5 分鐘 TTL，constant-time 比較
- **TokenAuth getter** — 每請求動態讀取 token，支援 runtime token 變更
- **PairingGuard middleware** — Quick 模式下只放行 `/api/pair/*`，其餘回 503
- **Base58 codec** — `internal/pairing/` 獨立 package，encode/decode 配對碼（9 bytes → 13 chars, 4-4-5 格式）
- **Pairing handlers** — `/api/pair/verify` + `/api/pair/setup`，brute-force 保護（10 次失敗 regenerate）
- **Token auth handler** — `/api/token/auth`，confirm 後持久化到 config.toml
- **`/api/health` mode 欄位** — 回傳 `{"ok":true,"mode":"pairing"|"pending"|"normal"}`
- **SPA AddHostDialog 重寫** — 配對碼 + Token 雙路線，stage state machine，IP:port 去重
- **SPA pairing-codec** — TypeScript Base58 解碼 + `purdex_` token 產生
- **i18n** — 新增 12 個配對相關翻譯 key（en + zh-TW）

### 安全性

- **persist-first** — `handlePairSetup` 先 WriteFile 成功才設 runtime token，失敗不污染 state
- **config.toml 權限** — `WriteFile` 改用 `0600`（原為 `0666`），保護明文 token
- **concurrent verify 序列化** — CfgMu.Lock + TOCTOU guard 防止並發 verify 覆蓋 setupSecret
- **PairingSecret 鎖保護** — CfgMu RLock/Lock 保護讀寫，消除 data race

### 重構

- `internal/core/base58.go` → `internal/pairing/base58.go`（獨立 package）
- `cmd/tbox/main.go` 配對初始化抽取至 `cmd/tbox/quick.go:initPairing()`
- PairingGuard 移除 OPTIONS dead code（CORS 已處理）

## [1.0.0-alpha.45] - 2026-04-04

Phase 4 Hotfix — SM tmuxState 覆寫 + L2 背景重連 + Test Connection 重連

### 修復

- **SM onStateChange 不覆寫 tmuxState** — `checkHealth` 的 tmux 是硬編碼 `unavailable`，不應覆蓋 WS event 推送的正確狀態
- **Test Connection 觸發 SM reconnect** — 成功時呼叫 `manualRetry()` 讓 WS 恢復連線
- **i18n** — 「測試連線」→「嘗試連線」

### 新功能

- **L2 refused 背景重連** — FAST_RETRY 後每 3 秒嘗試連線，3 分鐘後停止。Daemon 重啟後不再需要手動操作

## [1.0.0-alpha.44] - 2026-04-04

Phase 4 錯誤 UI — Terminated Tab + Host Error Display + Cascade Delete (PR #162)

### 新功能

- **Tab 狀態模型** — PaneContent `kind: 'session'` 改名為 `kind: 'tmux-session'`，新增 `terminated?: TerminatedReason` 欄位（event-sourced）
- **TerminatedPane 錯誤頁** — session 關閉 / tmux 重啟 / host 刪除三種情境，顯示對應訊息 + 關閉按鈕 + 跨 host SessionPickerList
- **SessionPickerList** — 列出所有已連線 host 的 session，按 host 分組，可用於 tab 重新綁定
- **deriveTabState** — 從 PaneContent + HostRuntime 推導 tab 顯示狀態（active / reconnecting / terminated）
- **Host 層級 L1-L3 錯誤 UI** — StatusBar / HostSidebar / OverviewSection / SessionsSection 各自顯示連線錯誤狀態
- **useHostConnection hook** — 封裝 ConnectionStateMachine 存取，提供 `manualRetry()` 手動重連
- **Reconnecting overlay 手動重連按鈕** — TerminalView 斷線覆蓋層新增重連按鈕 + spinner
- **Host 刪除 cascade cleanup** — checkbox 選擇是否關閉分頁，多 store cascade（AgentStore.removeHost + StreamStore.clearHost）+ 全域 undo toast（5s snapshot 復原）
- **NotificationAction 模組化** — 通知 click handler 改為 action payload dispatch 模式，支援 `open-session` / `open-host`
- **L2/L3 系統通知** — daemon refused / tmux unavailable 狀態轉換時發送桌面通知

### 重構

- **PaneContent kind rename** — `'session'` → `'tmux-session'`，含 Zustand persist migration v1→v2
- **connectionErrorMessage 共用** — 抽取到 `lib/host-utils.ts`，OverviewSection 和 SessionsSection 共用
- **Undo toast 全域化** — `useUndoToast` store + `GlobalUndoToast` 元件，跨頁面導航持續顯示

### 修復

- **#156** — AddHostDialog 顯示具體 L1/L2/401 錯誤訊息
- **#140** — TokenField 清空時不觸發驗證
- **health timeout** — checkHealth timeout 從 3s 調整為 6s

## [1.0.0-alpha.43] - 2026-04-03

Phase 3 連線偵測 — Watcher 狀態機 + WS ping/pong + useHostConnection 閘控 (PR #158)

### 新功能

- **Watcher 狀態機** — NORMAL/TMUX_DOWN 雙模式，tmux 斷線自動偵測 + broadcast `tmux` event，wait-for goroutine 在 TMUX_DOWN 時暫停
- **WS ping/pong** — host-events WS 加入 30s ping / 10s pong timeout，整合 write pump（one-writer rule）
- **API 三層分離** — `/api/health`（無 auth, liveness）、`/api/ready`（有 auth, readiness + tmux 狀態）、`/api/info`（有 auth, identity）
- **ConnectionStateMachine** — 純 class 重連狀態機，L1 不間斷背景重試 + L2 停止 + epoch counter 防止 stale callback
- **checkHealth** — AbortController 3s timeout，L1（unreachable）/ L2（refused）分類
- **WS 閘控** — host-events WS 停止自身 reconnect 由 SM 管理，terminal WS 受 `canReconnect` gate 閘控
- **HostRuntime 擴充** — 新增 `daemonState`（connected/refused/unreachable）+ `tmuxState`（ok/unavailable）
- **TmuxAlive** — Executor interface 新增 `TmuxAlive() bool`，RealExecutor 用 `tmux info`（5s timeout）

### 重構

- **Rename** — `SessionEvent` → `HostEvent`、`/ws/session-events` → `/ws/host-events`（Go + SPA 全面更新）

### 修復

- **notifyWaitFor drain** — 先清空 channel 再寫入，防止 stale signal 阻塞 resume
- **wstate 封裝** — `updateHash` accessor 取代 tickNormal 直接操作 mutex
- **SM stopped guard** — await 後檢查 stopped + epoch，防止 unmount 後 state mutation
- **reconnect ws close** — 重連前關閉既有 WS，防止 duplicate connection

## [1.0.0-alpha.42] - 2026-04-03

SPA 識別系統整合 — Phase 2b (PR #155)

### 新功能

- **PaneContent 擴充** — session kind 新增 `cachedName`（斷線後仍顯示名稱）+ `tmuxInstance`（偵測 tmux 重啟）
- **Daemon Host ID 整合** — AddHostDialog 連線成功後 fetch `/api/info` 取得 daemon 的 `host_id` 作為 store key
- **cachedName 即時同步** — WS session 更新時自動同步 tab 的 cachedName（rename 即時反映）
- **Tab label fallback 鏈** — live name → cachedName → sessionCode

### 修復

- **addHost dedup** — 重複 daemon `host_id` 不再造成 `hostOrder` 重複
- **updateSessionCache layout 安全** — 使用 `updatePaneInLayout` 取代 hardcoded leaf，保護未來 split pane 結構
- **notification cachedName** — 從 notification 重開 tab 時從 sessionStore 查 name，避免空窗期顯示 sessionCode

### Breaking Changes

- PaneContent session 新增 required 欄位，既有 persisted tabs 資料重置

## [1.0.0-alpha.41] - 2026-04-03

Daemon Host ID 產生 + /api/info 擴充 — Phase 2a (PR #153)

### 新功能

- **Host ID** — Daemon 啟動時產生穩定的 `hostname:6-char-code` 識別碼，持久化到 `config.toml`
- **/api/info 擴充** — 回傳 `host_id`（daemon 自報 ID）+ `tmux_instance`（`pid:startTime`，偵測 tmux server 重啟）
- **config.WriteFile** — 統一的 config 原子寫入函式（取代重複的 `persistConfig` / `writeConfig`）

### 修復

- **HostID rollback** — `EnsureHostID` 持久化失敗時回滾 `cfg.HostID`，避免使用不穩定的 ID
- **PUT /api/config redact** — 回應與 GET 一致，隱藏 `host_id` + `token`

## [1.0.0-alpha.40] - 2026-04-03

Storage 抽象層 + Key 遷移 — Phase 1a (PR #152)

### 新功能

- **Storage 抽象層** — 新增 `spa/src/lib/storage/` 模組，以 Zustand `StateStorage` 介面為基礎建立可替換的 storage backend
- **BrowserBackend** — localStorage 包裝 + BroadcastChannel 跨 tab 狀態同步
- **STORAGE_KEYS 常數** — 11 個 localStorage key 的 single source of truth

### 重構

- **Key 遷移** — 所有 10 個 persist store 的 key 從 `tbox-*` → `purdex-*`，統一使用 `STORAGE_KEYS` 常數
- **移除死碼 migrate** — useHostStore（v0→v1）、useSessionStore（v1→v2）、useTabStore（v1→v2）的 migrate 函式及 `addHostIdToLayout` helper（-92 行）
- **Version 統一** — 所有 store 重設為 `version: 1`

### 修復

- **Rehydration 迴圈** — `browserStorage.setItem` 加入 equality check，防止 `onRehydrateStorage` callback 觸發無限 BroadcastChannel ping-pong
- **BC null guard** — `sync.ts` onmessage 加入型別檢查，防止非預期訊息格式導致 TypeError

### Breaking Changes

- 所有 localStorage key 更名（alpha 階段不向下相容），升級後本地 persist 資料重置

## [1.0.0-alpha.39] - 2026-04-02

Unify session mode naming + remove JSONL mode (PR #151)

### 重構

- **Mode 命名統一** — Daemon 和 SPA 統一使用 `"terminal"` / `"stream"`（原 daemon 回傳 `"term"`，導致 StatusBar 顯示不一致）
- **移除 JSONL session mode** — 移除未使用的 JSONL mode（`JSONLConfig`、jsonl preset、jsonl icon），保留 CC 歷史記錄 `.jsonl` 檔案格式解析
- **DDL schema 同步** — SQLite `DEFAULT 'term'` → `'terminal'`
- **升級遷移** — `ResetStaleModes()` 啟動時自動將舊的 `"term"` / `"jsonl"` 記錄遷移為 `"terminal"`

### 清理

- **刪除 TopBar** — Phase 1 遺留的 deprecated 元件（已被 TabBar + StatusBar 取代，無任何引用）

## [1.0.0-alpha.38] - 2026-03-31

Host Page UI + Multi-Host Integration — Phase 1.6c C+D (PR #136)

### 新功能

- **Host Page** — 完整主機管理頁面（ActivityBar HardDrives 按鈕），含 sidebar accordion 導覽 + 4 個子頁面
- **Overview** — 連線設定（editable name/ip/port/token with validation）、Daemon Config（sizing mode）、System Info
- **Sessions** — Session CRUD table（New/Open/Rename/Delete）+ agent status badge
- **Hooks** — tmux hooks + agent hooks 安裝狀態、install/remove 操作、per-event indicator
- **Uploads** — 暫存檔案按 session 分組、stats 顯示、個別/批次刪除
- **Add Host dialog** — health check → 401 偵測 → token 輸入 onboarding 流程
- **Token editing** — show/hide toggle、儲存前 /api/sessions 驗證、401 error handling
- **Multi-host grouping** — SessionPanel + SessionSection 按 host 分組、single host 時隱藏 header
- **Offline handling** — SortableTab WifiSlash icon、StatusBar 三色狀態、離線 disable
- **ErrorBoundary** — React error boundary 防止全畫面崩潰
- **Electron crash recovery** — render-process-gone 自動重載 SPA

### 修正

- sizing_mode 選項值對齊 daemon（auto/terminal-first/minimal-first）
- isOffline 邏輯統一（runtime undefined = not offline）
- isActiveSession 檢查 activeHostId（multi-host 防衝突）
- getAgentStatus 改用 reactive hook（非 getState）
- StatusIcon grey for undefined runtime
- AddHostDialog stage reset 涵蓋 needs-token/error
- HooksSection schema 對齊 daemon（tmux_hooks map）
- EditableField double-save 防護（savedRef）
- ws?.close() null safety + onerror handler (#137)
- fetchHost unhandled promise .catch() (#137)
- i18n 全面覆蓋（9 hardcoded strings 修正）
- a11y: AddHostDialog aria-modal + Escape、Section aria-expanded、TokenField aria-label
- InlineRename onBlur 防止編輯卡住

### 測試

- 63 個新測試（7 個 test files），734/735 pass

### 追蹤 Issues

- #138 OverviewSection 拆檔
- #139 HostSidebar expanded 同步
- #140 TokenField 清除 UX
- #141 Terminal palette 顯示
- #142 Hook 最後觸發時間
- #143 Upload 暫存目錄編輯
- #144 SessionPanel host header 可收合

## [1.0.0-alpha.37] - 2026-03-31

Agent File Upload — 拖曳檔案上傳到 CC agent（PR #129）

### 新功能

- **Daemon upload endpoint** — `POST /api/agent/upload`，multipart 上傳 → 存到 `~/tmp/tbox-upload/{session}/` → `tmux send-keys` 注入路徑
- **TerminalView drag-drop** — CC agent 活躍時攔截拖曳，逐一上傳逐一注入，drop overlay 提示
- **StatusBar 上傳進度** — uploading（黃色 spinner + 檔名）/ done（綠色勾）/ error（紅色叉，可點擊消除）
- **Agent label badge** — 有 model name 時橘棕色 badge，fallback 白色帶框
- **useUploadStore** — per-session 上傳狀態管理（Zustand，不 persist）
- **i18n** — 上傳相關文字支援 en/zh-TW，含單複數處理

### 安全修復

- **Path traversal 防護** — `filepath.Base()` sanitize 上傳檔名
- **路徑引號包裹** — send-keys 注入路徑以雙引號包裹，處理含空格檔名

### 修正

- **並發 Drop 保護** — `dismiss()` 不清除 uploading 狀態，防止重複拖曳污染 store

### 追蹤 Issues

- #127 StatusBar agent label modelName 被 latest event 覆蓋
- #130 upload send-keys 失敗時清理孤立暫存檔案
- #131 deduplicateFilename TOCTOU race condition

## [1.0.0-alpha.36] - 2026-03-31

Agent Hook 增強 — error 狀態、subagent 追蹤、unread 修正（PR #123）

### 新功能

- **error 狀態** — `StopFailure` 推導為新的 `error` 狀態，紅色燈號（`#ef4444`），觸發 unread 紅點和桌面通知
- **Subagent 追蹤** — 註冊 `SubagentStart`/`SubagentStop` hooks，以 `agent_id` 追蹤 active subagents（ephemeral，不存 DB）
- **SubagentDots 元件** — tab icon 左側顯示 1-3 顆藍色呼吸燈（`#60a5fa`），依 subagent 數量遞減尺寸
- **通知 newline 壓縮** — 彈窗 body 連續換行壓成單個

### 修正

- **Unread 紅點不可見** — 移除 tab `overflow-hidden`，重新定位到右上角框線上（`-top-[4px] -right-[4px]`、`z-20`），不再被 close 按鈕 gradient 遮蔽
- **SessionStart 推導修正** — `startup`/`resume`/`clear` → `idle`（等待輸入），非 `running`
- **shouldNotify 遺漏 error** — `StopFailure` 現在正確觸發桌面通知
- **error 不被 idle Notification 降級** — `idle_prompt`/`auth_success` 不會覆蓋 error 燈號
- **HandoffButton error 支援** — `isAgentActive` 加入 `error`，StopFailure 後 Handoff 按鈕不會 disabled
- **SessionStart(compact) 不清空 subagents** — compact 是工作中途壓縮，subagent 可能還在跑
- **WS 重連清空 subagents** — `onOpen` callback 清除 ephemeral 追蹤，避免斷線後殘留
- **SessionStatusBadge 加 error 顏色** — `bg-red-500`

### 追蹤 Issues

- #124 PermissionRequest 可覆寫 error 狀態
- #125 HandoffButton isAgentActive error 測試
- #126 SubagentStop orphan event 覆寫 events map

## [1.0.0-alpha.35] - 2026-03-30

衍生 focusedSession + tab 切換 auto-focus（PR #122）

### 修正

- **通知在 active tab 仍彈出** — `focusedSession` 從手動同步改為從 `activeTabId` 即時衍生（`getActiveSessionCode()`），鍵盤快捷鍵切 tab 時通知正確抑制
- **隱藏 tab 攔截鍵盤輸入** — 非 active 的 keep-alive tab 加上 `inert` attribute，阻止 offscreen terminal 捕獲 focus
- **Stream tab 切換後自動 focus** — `StreamInput` 加 `focused` prop，切到 stream tab 時 textarea 自動 focus
- **通知點擊 active tab 未清 unread** — `handleNotificationClick` 補 `markRead` 保底

### 重構

- 移除 `focusedSession` / `setFocusedSession` 狀態，改用 cross-store subscription 自動 markRead
- 移除 `useTabWorkspaceActions` 中的手動 focus 同步邏輯

## [1.0.0-alpha.34] - 2026-03-30

防止 tbox setup 重複 hook entries（PR #120）

### 修正

- **setup 防重複 hook** — 從不同路徑執行 `tbox setup`（如 `./tbox` vs `./bin/tbox`）不再累積重複 entries，每次 setup 先清除所有既有 tbox entries 再加入當前路徑
- **`entryIsTbox` 精確比對** — 用 binary basename 邊界檢查（`/tbox"` / `/tbox `）取代寬鬆的 `Contains`，避免誤刪 `tbox-extra` 等第三方工具的 hooks
- **移除死碼** — 清除不再使用的 `hasTboxEntry`、`filterOutTbox`、`entryMatchesTbox`
- **測試 `expectedEvents` 改用 `hookEvents`** — 補齊遺漏的 `StopFailure` 事件覆蓋

## [1.0.0-alpha.33] - 2026-03-30

移除 tab 燈號 fallback（PR #119）

### 修正

- **移除無 snapshot 時的 idle fallback** — 原本 hooksInstalled 為 true 時預設顯示灰色燈號，無法區分「agent 在跑但尚未送出 event」與「沒有 agent」，現在只在收到實際 hook event 後才顯示燈號

## [1.0.0-alpha.32] - 2026-03-30

通知去重 + 視窗焦點感知（PR #118）

### 修正

- **通知持久化去重** — 用 localStorage `lastSeenTs`（Infinity sentinel）取代 in-memory `notifiedRef`，跨重啟不重複通知
- **視窗焦點感知** — 只在 App 視窗有焦點且正看該 tab 時抑制通知，App 在背景時仍發通知
- **SessionEnd 清理** — 清除 `lastSeenTs` 防止 session code 重用時舊 ts 阻擋通知
- 三層 dedup 架構文件化（localStorage → shouldNotify focus → Electron main recentBroadcasts）

## [1.0.0-alpha.31] - 2026-03-30

Hook 引號路徑匹配修復（PR #117）

### 修正

- **`findTboxCommand` 支援引號路徑** — strip `"` 後匹配 `tbox hook`，修復 `hooksInstalled` 在引號路徑下回傳 false 的問題

## [1.0.0-alpha.30] - 2026-03-30

SPA 來源切換 Preflight（PR #116）

### 修正

- **forceLoadSPA preflight** — 切換至 Dev Server 前先驗證可達性（2s timeout + `response.ok`），避免用戶困在錯誤頁
- IPC error 序列化為 plain string（contextBridge 相容）
- 錯誤訊息顯示具體原因 + i18n 化

## [1.0.0-alpha.29] - 2026-03-30

Dev Update 包含 Renderer（PR #115）

### 修正

- **Dev update 包含 renderer** — download tar 現在打包 `out/renderer/`，updater 也替換它，SPA 改動不再需要重裝 `.app`
- Rollback 各目標獨立 try-catch，防止連鎖失敗
- 測試 tar reader 區分 `io.EOF` 和真實錯誤

## [1.0.0-alpha.28] - 2026-03-30

Electron CORS 修復 + SPA 來源切換（PR #113）

### 新增

- **`app://` custom protocol** — bundled SPA 改用 `app://` 取代 `file://`，啟用標準 CORS 行為
- **SPA Source 顯示** — Development Settings 顯示當前 SPA 來源（Dev Server / Bundled）
- **SPA 來源切換** — 一鍵切換 Dev Server ↔ Bundled，即時 reload
- **`forceLoadSPA` IPC** — Electron preload 暴露（`TBOX_DEV_UPDATE` gate 內）

### 修正

- Protocol handler 路徑遍歷防護（`startsWith` 驗證 + 403）
- `forceLoadSPA` 改 async、await `loadURL`、IPC handler return promise
- `spaSource` 偵測改正向匹配 `app:` protocol

## [1.0.0-alpha.27] - 2026-03-30

Agent Hook 子類別狀態判斷（PR #107）

### 新增

- **`deriveStatus` 子類別判斷** — Notification 依 `notification_type`、SessionStart 依 `source` 精確判斷狀態
- **`StopFailure` 事件處理** — daemon 註冊 hook + SPA 狀態映射（idle）+ 桌面通知（error_details）
- **`hooksInstalled` fallback** — hooks 已安裝但尚無事件的 session tab 預設顯示 idle 狀態點
- **CC Hook Event Reference 文件** — `docs/cc-hook-event-reference.md`，完整事件映射與設計決策

### 修正

- `Notification(idle_prompt)` 不再覆蓋 `Stop` 設定的 idle 狀態為 waiting
- `SessionStart(compact)` 背景壓縮不再錯誤觸發 running 狀態
- `shouldNotify` 排除 idle Notification（避免 idle_prompt 重複通知）
- `useNotificationDispatcher` 傳入 `rawEvent` 給 `deriveStatus`（修正通知靜默 regression）
- `useHookStatus` install/remove 後同步 `hooksInstalled` 到全域 store
- `App.tsx` hook-status fetch 加入 `res.ok` 檢查
- 未知 `notification_type` 記錄 console.warn
- Unread dot 排除資訊性 Notification（idle_prompt / auth_success）
- 新增 `notification.fallback.stopFailure` i18n key（en + zh-TW）

## [1.0.0-alpha.26] - 2026-03-29

1.6c-pre2: CC 通知系統（PR #102）

### 新增

- **Electron 系統通知** — Agent hook 事件（waiting + idle）觸發 macOS 系統通知，點擊跳轉對應 tab
- **`agent_type` 欄位** — `tbox hook --agent cc` 識別 agent 類型，daemon 儲存並廣播
- **`broadcast_ts` 持久化** — 多視窗去重 + 防止 WS 重連通知爆發
- **Agent Settings section** — Settings 內新增 Agent 區塊，per-agent 通知開關 + per-event toggle
- **Hook 狀態檢視** — Agent Settings 顯示 hook 安裝狀態 + 一鍵安裝/移除按鈕
- **`GET /api/agent/hook-status`** — 讀取 CC settings.json 回報 hook 安裝狀態
- **`POST /api/agent/hook-setup`** — 執行 `tbox setup` 安裝或移除 hook
- **`useNotificationDispatcher`** — SPA 通知判斷 + Electron/PWA 雙路徑分發
- **`useNotificationSettingsStore`** — Per-agent 通知設定（Zustand + persist）
- **`useHookStatus`** — Hook 狀態查詢 custom hook
- **i18n** — 新增 19 個 Agent 相關 locale key（en + zh-TW）
- **三層級設定預留** — system → host → workspace 覆寫架構（先做 system 層）

### 修正

- `focusedSession` 切到非 session tab 時正確清除
- 通知點擊重開 tab 時正確加入 workspace
- 多視窗通知點擊只聚焦有 tab 的視窗（不閃現所有視窗）

## [1.0.0-alpha.25] - 2026-03-29

Dev Update Auto-Build（PR #96）

### 新增

- **`out/.build-info.json`** — `electron-vite build` 完成後寫入 build metadata（version + hash + timestamp）
- **check/download 一致性** — `check` 端點改讀 `.build-info.json` 作為 build hash，頂層回傳 build hash + `source` 回傳 git hash
- **Auto-build** — source ≠ build 時 daemon 背景自動觸發 `electron-vite build`，回傳 `building: true`
- **Build 失敗退避** — 同一 source hash 失敗後不重複觸發，source 改變才重試
- **Download 建置鎖** — build 進行中 download 回傳 409 Conflict
- **SPA Building 狀態** — 顯示「建置中…」+ 每 3 秒 poll，完成後自動比對

### 修正

- Partial build 不再污染 `.build-info.json`（build 前先刪除）
- `pnpm exec` 取代 `npx`，符合 pnpm-only 規範
- `RemoteInfo` 型別從 `electron.d.ts` derive，消除三處重複定義

### 關閉 Issue

- #78（feat: dev update — auto-build before download）
- #98（refactor: unify RemoteVersionInfo type）

## [1.0.0-alpha.24] - 2026-03-29

Agent Hook 狀態偵測（PR #91）

### 新增

- **`tbox hook` 子命令** — CC hook 觸發時讀取 stdin + tmux session name，POST 到 daemon `/api/agent/event`
- **`tbox setup` 子命令** — 自動配置 `~/.claude/settings.json` hook entries（冪等、支援 `--remove`）
- **Agent module（daemon）** — 純 relay：存 raw event + WS broadcast，不解析 payload
- **AgentEventStore** — 獨立 SQLite 儲存每 session 最近一筆 hook event，新 WS subscriber 自動 snapshot
- **useAgentStore（SPA）** — hook event → running/waiting/idle 狀態機 + unread 管理
- **TabStatusDot** — 三種 tab 指示器樣式（overlay / replace / inline），Settings 可切換，預設 overlay
- **呼吸燈動畫** — running 狀態 `background-color` fade 到 tab 底色
- **未讀紅點** — 5px 暗紅圓點在 inactive tab 右上角
- **Session Panel 燈號** — 狀態 dot 在 code 前方（running/waiting/idle）
- **StatusBar agent 資訊** — 有 agent 時顯示名稱（`getAgentLabel` 集中化）

### 變更

- **移除 CC status poller** — 完全移除 `poller.go`，CC 狀態改為 hook 驅動
- **WS "hook" 事件取代 "status"** — `SessionEvent.type` 不再包含 `'status'`
- **SessionStatusBadge** — 改用 `AgentStatus`（running/waiting/idle），非 agent session 不顯示 badge
- **HandoffButton** — `sessionStatus` prop 改為 `agentStatus`，語意等價
- **useStreamStore** — 移除 `sessionStatus` 欄位，agent 狀態獨立管理

### 修正

- Hook POST 加入 `Authorization: Bearer` header（token 環境下不再 401）
- TabStatusDot running 加 fallback `backgroundColor`（CSS animation 不生效時仍可見）
- `tbox setup` 路徑含空格時加引號、`entryMatchesTbox` 改用 `HasPrefix` 避免誤刪
- 空 `tmux_session` 不存入 DB（避免 garbage row）
- SortableTab 抽出 `renderTabIcon` 消除 pinned/normal 重複邏輯

## [1.0.0-alpha.23] - 2026-03-28

Dev update 進度回饋（PR #85）

### 新增

- **Update 進度顯示** — 點 Update App 後即時顯示 Downloading → Extracting → Applying 各階段
- **錯誤訊息顯示** — 更新失敗時顯示具體錯誤（之前完全無回饋）
- **`dev:update-progress` IPC** — main process 透過 push 事件回報步驟

### 修正

- Error 跨 contextBridge 序列化失敗 — 改在 main process catch 後轉為 string re-throw
- 加入 `updateInProgress` lock 防止重複呼叫 `applyUpdate` 導致檔案競態
- 移除不可達的 `progress('restarting')` 呼叫（app.exit 前 IPC 來不及送達）

## [1.0.0-alpha.22] - 2026-03-28

Electron 快捷鍵系統 + tear-off 修正（PR #84）

### 新增

- **Keybinding registry** — `electron/keybindings.ts` 集中定義快捷鍵，`menuGroup` 分組，為未來自定義擴充預留
- **Electron Menu** — App / File / Edit / Tab / View 五層選單，含快捷鍵提示
- **快捷鍵** — `Cmd+T` 新增分頁、`Cmd+N` 新增視窗、`Cmd+1~9` 切換 tab、`Cmd+Option+←/→` 前後切換、`Cmd+,` Settings、`Cmd+Y` History、`Cmd+Shift+T` 重開 tab
- **useShortcuts hook** — 統一 `shortcut:execute` IPC listener，workspace-aware tab 切換
- **17 個單元測試** — 含 workspace 整合、邊界情況

### 修正

- **Tear-off 帶走所有 tab** — 新視窗從 localStorage persist 恢復出全部 tab。改用 `replace` 旗標，tear-off 時清空再加入
- **reopen-closed-tab 不加入 workspace** — 重開的 tab 現在加入 active workspace
- **prev-tab/next-tab 用全域 tabOrder** — 改用 workspace visible tabs，與 TabBar 顯示一致
- 移除 `App.tsx` 硬編碼 `Cmd+Shift+T`，統一由 Menu accelerator 驅動

## [1.0.0-alpha.21] - 2026-03-27

Dev auto-update system（PR #77）

### 新增

- **Daemon dev module** — `/api/dev/update/check` + `/api/dev/update/download` 端點，`[dev] update = true` config 控制
- **Build hash 注入** — `__APP_VERSION__`、`__ELECTRON_HASH__`、`__SPA_HASH__` 透過 Vite define 編譯時注入
- **Electron updater** — 下載 tar.gz、解壓、備份 + rollback、替換 out/、重啟
- **Settings「Development」section** — 版本資訊 + 檢查更新 / 更新 App / 重新載入 SPA
- **啟動時背景檢查** — main process 啟動後靜默查詢 daemon 有無新版

### 修正

- `devUpdateEnabled` 改由 `TBOX_DEV_UPDATE` 環境變數控制（preload 條件性暴露 IPC）
- 更新流程加入 backup + rollback 防止 partial update 損壞

## [1.0.0-alpha.20] - 2026-03-26

Electron shell — 桌面應用完整實作（PR #76）

### 新增

- **Electron desktop shell** — electron-vite + pnpm workspace monorepo 架構
- **多視窗管理** — tear-off / merge via context menu
- **WebContentsView browser pane** — 生命週期管理（ACTIVE → BACKGROUND → DISCARDED）
- **System tray** — 最小化到系統匣
- **Memory monitor** — process metrics 監控頁面

## [1.0.0-alpha.19] - 2026-03-26

PWA + Platform capabilities + Browser pane（PR #75）

### 新增

- **PWA installability** — manifest.json + icons（192/512/maskable）+ Apple meta tags
- **Platform capabilities** — `getPlatformCapabilities()` + ambient `electron.d.ts` 型別宣告
- **PaneContent `browser` kind** — labels + route mapping + i18n keys
- **NewTabProvider disabled 支援** — disabled provider 顯示說明文字

## [1.0.0-alpha.18] - 2026-03-25

i18n 系統 — 自建多語系 + 自訂語系 + 編輯器/匯入（PR #72）

### 新增

- **Locale Registry** — Map-based，與 Theme Registry 同架構
- **useI18nStore** — `t(key, params?)` 翻譯函式，persist `tbox-i18n`
- **Fallback chain** — active locale → en → key itself
- **LocaleEditor / LocaleImportModal** — fork builtin → 編輯 → 另存
- **locale-completeness.test.ts** — en/zh-TW key 完全對稱守門測試

## [1.0.0-alpha.17] - 2026-03-25

Theme 系統 — 多主題 + 自訂主題 + 匯入匯出（PR #71）

### 新增

- **23 語義 CSS token** — Tailwind v4 `@theme` + CSS Variables，分 6 組
- **4 預設主題** — Dark / Light / Nord / Dracula
- **Theme Registry** — Map-based + Zustand Theme Store（localStorage persist）
- **ThemeEditor** — 即時預覽 + fork / export / import
- **ThemeInjector** — runtime 注入自訂主題 `<style>`

## [1.0.0-alpha.16] - 2026-03-24

Settings UI — VSCode 風格 sidebar + content pane（PR #70）

### 新增

- **Settings pane** — 取代 overlay，以 singleton tab 呈現
- **Settings Section Registry** — 動態註冊，新增 section 只需 2 檔
- **通用元件** — SegmentControl / ToggleSwitch / SettingItem
- **Appearance section** — Theme / Language（disabled, 待 Phase 2/3）
- **Terminal section** — Renderer / Keep-alive / Reveal Delay

## [1.0.0-alpha.15] - 2026-03-24

Tab/Session 解耦 + Pane 模型 + wouter 路由（PR #69）

### 新增

- **Tab/Session 解耦** — Tab 從 Session 1:1 容器改為通用容器
- **Pane 模型** — PaneLayout tree + PaneContent discriminated union（new-tab / session / dashboard / history / settings）
- **Pane Registry** — `registerPaneRenderer(kind, { component })`
- **NewTab Provider Registry** — 可擴充的 content picker
- **wouter 路由** — hash → path-based（`/t/:tabId/:mode`、`/w/:workspaceId`）
- **useRouteSync** — 雙向路由同步（Tab ↔ URL）

## [1.0.0-alpha.14] - 2026-03-24

整合 CC + Stream modules，刪除 legacy server（PR #68）

### 變更

- **Module 整合** — `cc.New()` + `stream.New()` 接入 main.go
- **Legacy 清除** — 刪除 `internal/server/`（18 檔、~4600 LOC）+ legacy store + migration
- **Session code 統一** — SPA 全面從 session name 改用 session code

## [1.0.0-alpha.13] - 2026-03-23

Phase 1.6b Tasks 9-10 — Stream module（PR #66）

### 新增

- **Stream module** — relay WS 管理、SPA subscriber fan-out、handoff 編排
- **Bridge 改用 session code** 作為 key
- **Handoff 改用 CCOperator methods** — 取代重複的 raw tmux send-keys

## [1.0.0-alpha.12] - 2026-03-23

Phase 1.6b Tasks 1-8 — Core 擴充 + CC module（PR #65）

### 新增

- **Core 擴充** — Module `Dependencies()` + `Stop(ctx)`、Kahn's algorithm 拓撲排序
- **EventsBroadcaster** — fire-and-forget + OnSubscribe snapshot
- **Config handler** — OnConfigChange callback
- **CC module** — CCDetector + CCOperator + CCHistoryProvider + Status Poller
- **Middleware 搬遷** — `internal/middleware/` 獨立 package

## [1.0.0-alpha.11] - 2026-03-22

Phase 1.6b Part 1 — Core 擴充 + CC module 基礎（PR #63）

### 新增

- Core layer 擴充基礎建設
- CC module 初始結構（後續 PR #65 完成）

## [1.0.0-alpha.10] - 2026-03-22

修復 MetaStore 冗餘寫入（PR #60）

### 修正

- Handoff Step 8 移除 `SetMeta` 後重複的 `UpdateMeta`
- `MigrateFromLegacy` 錯誤改為 log 輸出

## [1.0.0-alpha.9] - 2026-03-22

Phase 1.6a — Daemon Module 架構 + Session 重設計（PR #59）

### 新增

- **Module 架構** — Core + ServiceRegistry + Module interface，支援可插拔模組
- **Session 重設計** — tmux 為 SOT，DB 降級為 meta cache
- **Session ID 編碼** — 6 碼 base36 code（multiplicative cipher）

## [1.0.0-alpha.8] - 2026-03-22

修復 Tab 拖曳右邊界（PR #58）

### 修正

- Tab 拖曳右邊界限制在最後一個 tab，不再進入 + 按鈕區域

## [1.0.0-alpha.7] - 2026-03-22

Pin/Lock 獨立化（PR #57）

### 變更

- Pin 和 Lock 解耦為獨立旗標：pin 只負責定位，lock 只負責擋關閉
- Pinned tab 可被關閉（除非同時 locked）
- Reopen 恢復 pinned 狀態

## [1.0.0-alpha.6] - 2026-03-21

Tab 互動強化 — 拖曳排序 + 溢出箭頭 + 右鍵選單（PR #56）

### 新增

- **拖曳排序** — @dnd-kit 雙區（pinned / normal）+ restrictToTabZone modifier
- **溢出箭頭** — tab 超出可視範圍時顯示左右捲動按鈕
- **右鍵選單** — Pin / Lock / Close / Close Others
- **中鍵關閉** — 中鍵點擊 tab 關閉

## [1.0.0-alpha.5] - 2026-03-20

Phase 1.5 Task 2 — TerminalView 拆分 + Keep-Alive（PR #55）

### 新增

- **TerminalView 拆分** — 222 → 79 行，抽出 `useTerminal` + `useTerminalWs` hooks
- **useTabAlivePool** — LRU keep-alive pool 管理
- **TabContent pool 渲染** — `display: none` 隱藏非活躍 tab

## [1.0.0-alpha.4] - 2026-03-20

Phase 1.5 Task 1 — Tab 模型擴充（PR #54）

### 新增

- Tab interface 加入 `pinned` / `locked` 欄位
- useTabStore 新增 pin / unpin / lock / unlock 方法
- `removeTab` / `dismissTab` 加入 locked guard

## [1.0.0-alpha.3] - 2026-03-20

Phase 1.1 — Tab 模型修正 + view toggle（PR #48）

### 變更

- Tab 模型從封閉 union 改為開放式 `type: string` + `viewMode` + `data` bag
- 新增 Tab Renderer Registry
- 還原 v0 的檢視/handoff 分離設計

## [1.0.0-alpha.2] - 2026-03-20

xterm addons + terminal renderer toggle（PR #47）

### 新增

- **@xterm/addon-unicode11** — CJK 字元寬度支援
- **@xterm/addon-web-links** — 可點擊 URL
- **Terminal 渲染器切換** — WebGL / DOM 下拉選單

## [1.0.0-alpha.1] - 2026-03-20

Phase 1: 分頁系統 + Activity Bar — SPA 架構從「單 session 檢視」升級為「多分頁 + 工作區」

### 新增

- **Tab 系統** — 每個 tmux session 自動對應一個 tab，支援 terminal / stream / editor 三種類型
- **ActivityBar** — 左側垂直工作區切換列（Workspace icons + standalone tabs + 設定入口）
- **TabBar** — 水平分頁列（切換 / 關閉 / 新增 / dirty indicator）
- **TabContent** — 只掛載 activeTab，切換即銷毀重建（keep-alive 因 tmux resize corruption + WebGL 耗盡移除）
- **StatusBar** — 底部狀態列（host / session / mode）
- **SessionPicker** — Session 選擇 popover（搜尋 + 已開啟標記）
- **useTabStore** — Tab CRUD + `dismissTab` 防止關閉的 tab 被 auto-sync 復活 + localStorage 持久化
- **useWorkspaceStore** — Workspace 管理 + tab 歸屬 + per-workspace activeTab
- **useHostStore** — 取代 hardcoded daemonBase（最小版，Phase 6 擴充為多主機）
- **useUISettingsStore** — 前端 UI 設定（terminalRevealDelay 300ms + terminalRenderer webgl/dom）
- **useIsMobile** — 響應式 breakpoint hook（768px）
- **Hash routing** — `#/tab/{tabId}` 格式，支援 back/forward + 重整後保留
- **App.tsx 重構** — 提取 `useSessionEventWs`、`useSessionTabSync`、`useHashRouting` 三個 custom hooks（345→247 行）
- **xterm.js addons** — `@xterm/addon-unicode11`（CJK 字元寬度）+ `@xterm/addon-web-links`（可點擊 URL）
- **Terminal 渲染器切換** — Settings 新增 WebGL / DOM 下拉選單，變更後自動重連

### 修正

- **crypto.randomUUID fallback** — 非 localhost HTTP context 無法使用，加了 Date.now + Math.random fallback
- **Terminal reveal delay 設定化** — 從 hardcoded 300ms 改為 `useUISettingsStore` 可調整，用 ref + subscribe 避免設定變更觸發 terminal 重建
- **Reconnect overlay 回歸修復** — 恢復 `if (revealed) setReady(true)` 讓 WS 重連後立即顯示 terminal
- **Stale tab 清理** — sessions 消失時自動移除對應 tab（guard `sessions.length > 0` 防止初始渲染清空）
- **Subscribe 洩漏修復** — TerminalView 的 Zustand subscribe 移入 useEffect + cleanup
- **Lint + type errors 全面修正** — 移除 `as any`、修正 SessionStatus type、補 missing fields

### 已知限制

- keep-alive 已移除，每次切 tab 都重新建立 terminal WS 連線（TerminalView 的 `visible` 路徑保留供未來 LRU 快取）
- StatusBar 狀態固定顯示 'connected'（未接 relayStatus/sessionStatus）
- TopBar 標記 @deprecated 但未刪除
- useIsMobile hook 已建立但未在任何元件中使用（Phase 7b）

## [0.5.4] - 2026-03-19

修復 handoff 相關的 terminal resize 與 copy-mode 問題

### 修復

- **Handoff 後 tmux 自動 resize 恢復** — `tmux resize-window -x 80 -y 24`（handoff step 3.5）會讓 tmux 進入手動尺寸模式，導致回到 term 後 window 卡在 80x24 不隨瀏覽器 viewport 調整。handoff 完成 `/status` 擷取後立即呼叫 `resize-window -A` 清除手動旗標
- **Handoff 前退出 tmux copy-mode** — terminal 處於 copy-mode（捲動瀏覽歷史）時 handoff 會失敗。改用 `tmux send-keys -X cancel` 取代依賴 `Escape`，不受 vi/emacs mode 影響且不送按鍵到底層應用

### 新增

- **`[terminal] auto_resize` 設定** — 預設啟用，每次 terminal WS 連線時自動清除手動尺寸旗標。使用者可設 `auto_resize = false` 停用
- **`Executor.ResizeWindowAuto`** — 封裝 `tmux resize-window -A`
- **`Relay.OnStart` callback** — PTY 啟動後的 hook，用於 terminal 連線時重設視窗尺寸

## [0.5.2] - 2026-03-19

架構重構：Stream UI 狀態改由 server-derived relayStatus 驅動

### 重構

- **ConversationView 改用 relayStatus** — 不再依賴 ephemeral `handoffState`，改為 `relayStatus[session]` 作為 single source of truth。Page refresh / WS 重連後自動恢復 stream UI 狀態
- **移除 handoffState** — store 中的 `HandoffState` type、`handoffState` map、`setHandoffState` action 全部移除
- **HandoffButton 簡化** — props 從 `state: HandoffState` 改為 `inProgress: boolean`

### 新增

- **TerminalView `visible` prop** — 切回 term tab 時自動 refit + resize，用遮罩擋住 500ms 等待 tmux 調整完畢再 fadeout

### 修復

- **Handoff 前退出 copy-mode** — 發送 Escape + C-u 退出 tmux 捲動模式並清空輸入，避免 send-keys 注入失敗
- **Handoff Escape error check** — SendKeysRaw(Escape) 失敗時提早返回

## [0.5.1] - 2026-03-18

Bugfix: Handoff tmux target、pane resize、xterm.js 選取

### 修復

- **Handoff tmux target 解析** — 所有 tmux 操作改用 `sess.TmuxTarget`（session:window 格式），避免 bare session name 被 tmux 模糊解析到錯誤的 pane
- **Handoff pane resize** — xterm.js 在 `display:none` 時 PTY 尺寸過小（10x5），tmux smallest-client 規則縮小 pane，`/status` TUI 渲染錯亂。relay PTY 預設 80x24，handoff 前檢查 pane 尺寸並 resize window
- **xterm.js 文字選取** — 啟用 `macOptionClickForcesSelection` 和 `rightClickSelectsWord`，抑制 terminal container 的右鍵選單

## [0.5.0] - 2026-03-18

Stream Message UI — 完整渲染所有 CC 訊息類型

### 新增

- **ThinkingBlock** — 可摺疊的 thinking 區塊（Brain icon，collapsed by default）
- **ToolResultBlock** — 可摺疊的 tool result 顯示（CheckCircle/XCircle 區分成功/錯誤）
- **Slash command 氣泡** — `/exit`、`/status` 等指令以黃棕色氣泡顯示（TerminalWindow bold icon）
- **Interrupted 提示** — 中斷訊息靠左紅棕色顯示（Prohibit icon）
- **@tailwindcss/typography** — 啟用 prose markdown 樣式

### 修改

- **MessageBubble** — User: 藍色氣泡靠右；Assistant: 移除氣泡，直接 prose markdown 輸出（Cowork 風格）
- **ToolCallBlock** — 統一 Wrench icon（移除 per-tool icons），新增 Agent/Grep/Glob summary
- **ConversationView** — 接線所有 content block 類型（thinking、tool_use、tool_result、text、command、interrupted）

### 修復

- **ParseJSONL 過濾 CC 內部標記** — 跳過 `isMeta`、`<local-command-caveat>`、`<local-command-stdout>`、`<synthetic>` assistant；解析 `<command-name>` 為乾淨文字
- **aria-expanded** — 所有可摺疊元件加入無障礙屬性

## [0.4.2] - 2026-03-18

Bugfix: 從 CC `/status` 取得 cwd，修復空 cwd session 的歷史載入

### 新增

- **`detect.ExtractStatusInfo`** — 從 CC `/status` 同時解析 Session ID 和 cwd
- **`store.SessionUpdate.Cwd`** — 支援更新 session 的 cwd 欄位

### 修復

- **空 cwd 導致歷史載入失敗** — auto-scan 使用 `#{session_path}` 取得 cwd，但部分 tmux session 該值為空，導致 history handler 無法定位 JSONL 檔案。改為在 handoff 時從 CC `/status` 輸出取得 cwd 並寫入 DB
- **cwdRegex 空白行誤匹配** — `cwd:` 行僅含空白時不再匹配為有效路徑

## [0.4.1] - 2026-03-18

Bugfix: Handoff 狀態管理修正

### 修復

- **Stream→Term handoff 後 stream 頁面狀態錯誤** — handoff 完成後 `handoffState` 錯留在 `'connected'`，切回 stream tab 時顯示無法互動的對話 UI 而非 HandoffButton。現在根據 session mode 判斷，term handoff 後正確重置為 `'idle'`
- **Term→Stream handoff 載入對話歷史** — `fetchSessions` 改為 await，確保用 fresh session data（含 `cc_session_id`）取得歷史。同時移除 `msgs.length > 0` 條件，空歷史也正確覆蓋避免舊 messages 殘留
- **Relay 關閉時的誤觸事件** — `runHandoffToTerm` 在關閉 relay 前先更新 DB mode 為 `"term"`，防止 `revertModeOnRelayDisconnect` 發送假的 `"failed:relay disconnected"` 事件
- **Handoff 失敗後的 mode rollback** — `runHandoffToTerm` 的 pre-update 在後續步驟失敗時會 rollback mode 到原始值，避免留下不一致的 DB 狀態
- **Term handoff 後清理 per-session state** — 切回 term 時呼叫 `clearSession` 清除上一輪 stream 的 messages、cost、sessionInfo
- **fetchSessions 失敗時的 fallback** — 從 `'connected'`（可能導致無法互動的 UI）改為 `'idle'`（安全預設，顯示 HandoffButton 讓使用者重試）

## [0.4.0] - 2026-03-18

Phase 2.5b: Stream WS Lifecycle Redesign — 修復 stream 訊息不通的根因

### 新增

- **Per-session store** — `useStreamStore` 從全域單例改為 `Record<string, PerSessionState>`，切換 session 不再丟失對話
- **useRelayWsManager hook** — relay 事件驅動 WS 生命週期（relay:connected → 建立 WS，relay:disconnected → 關閉 WS）
- **Relay 事件廣播** — session-events WS 新增 `relay` 事件類型 + snapshot，冷啟動單一資料源
- **Init metadata 攔截** — bridge handler 捕獲 CC init message 的 model 資訊存 DB
- **JSONL history API** — `GET /api/sessions/{id}/history` 讀取 CC 的 JSONL 檔案，resume 時顯示之前的對話
- **SessionResponse DTO** — session list API 回傳 `has_relay` + `cc_model`
- **`cc_model` DB 欄位** — sessions 表新增 cc_model 欄位 + migration
- **`GetSessionByName`** — store 新增 O(1) name 查詢方法
- **`RelaySessionNames`** — bridge 新增列舉所有有 relay 的 session 方法
- **`fetchHistory`** — SPA API client 新增歷史訊息查詢函式

### 修復

- **幽靈連線根因修復** — ConversationView 不再管理 WS 連線，改為純 UI 元件從 per-session store 讀取狀態
- **WS 生命週期脫鉤** — WS 建立/銷毀完全由 relay 事件驅動，不再依賴 component mount 時機
- **set() 內 side effect** — clearSession 的 conn.close() 移到 set() 外避免 re-entrant mutation
- **selector 穩定性** — 使用 stable 空陣列常數避免 Zustand `?? []` 造成的無限 render loop
- **subscribeWithSelector** — store 加入 Zustand middleware 支援 relay status 訂閱

### 改善

- ConversationView props 簡化為 `sessionName`（移除 `wsUrl`、`sessionStatus`）
- session-events type 擴充為 `'status' | 'handoff' | 'relay'`
- bridge 測試恢復 4 個被刪除的單元測試

## [0.3.0] - 2026-03-18

Phase 2.5a: Stream Handoff — 雙向切換

### 新增

- **Stream Handoff** — term（互動式 CC）與 stream（`-p` 串流模式）之間的雙向 handoff
- **SendKeysRaw** — tmux 控制鍵注入（C-u, C-c, Escape 不帶 Enter）
- **ExtractSessionID** — 解析 CC `/status` 輸出的 Session ID（UUID regex）
- **cc_session_id** — sessions 表新欄位 + migration + CRUD
- **Handoff 8 步流程** — CC 偵測 → 中斷 → `/status` 取 ID → `/exit` 退出 → relay `--resume`
- **Handoff to Term** — 6 步反向 handoff（shutdown relay → shell → `claude --resume`）
- **HandoffButton** — CC 狀態感知、進度標籤、disabled 狀態
- **StreamInput "Handoff to Term"** — 底部操作按鈕
- **E2E pipeline 測試** — SPA→bridge→relay→subprocess→bridge→SPA 完整往返驗證
- **Relay 斷線自動 revert** — session mode 自動回 term
- **session-events snapshot** — 新 subscriber 收到初始狀態快照

### 修復

- 混合式 CC 偵測（子程序樹 + pane content fallback）
- relay command 使用 config bind address
- `--verbose` 加入 stream-json preset（CC 2.1.77+ 要求）

## [0.2.0] - 2026-03-17

Phase 2: Stream 模式 — Claude Code 結構化互動

### 新增

- **StreamManager** — `claude -p` 子程序生命週期管理（spawn / stop / pub-sub stdout）
- **WebSocket `/ws/stream/{session}`** — 雙向 NDJSON 中繼（write mutex 保護）
- **Mode Switch API** — `POST /api/sessions/{id}/mode`（term ↔ stream 切換）
- **Store.GetSession** — 單一 session 查詢
- **ConversationView** — 結構化對話渲染（markdown / 程式碼高亮 / 自動捲動）
- **MessageBubble** — user / assistant 訊息氣泡（react-markdown + rehype-highlight）
- **ToolCallBlock** — 可摺疊工具呼叫區塊（工具圖示 + 摘要）
- **PermissionPrompt** — Allow / Deny 按鈕（`can_use_tool` control_request）
- **AskUserQuestion** — radio / checkbox 選項（支援完整 protocol 格式）
- **StreamInput** — 底部訊息輸入框
- **TopBar** — session 名稱 + 三模式按鈕（term / jsonl / stream）+ Stop 按鈕
- **SessionPanel 更新** — Phosphor Icons 狀態燈號 + 底部 Settings 入口
- **useStreamStore** — stream 模式狀態管理（messages / control requests / cost）
- **stream-ws** — 訊息型別定義 + WebSocket 連線管理（含 interrupt / sendControlResponse）

### 修復

- StreamSession：readLoop 中呼叫 cmd.Wait()（防止 zombie process）
- StreamSession：Unsubscribe 關閉 channel（防止 goroutine 洩漏）
- StreamSession：Send 使用 Lock（防止 stdin write race）
- Delete handler：同時停止 stream session（防止子程序洩漏）
- SwitchMode：UpdateSession 錯誤處理 + 回滾
- main.go：st.Close() 改用 defer 保護
- switchMode API：POST 方法（修正 PUT → POST）
- isStreaming 語意：僅在使用者送訊息時啟用（非 WebSocket open）
- AskUserQuestion：回應格式符合 STREAM_JSON_PROTOCOL（含 questions + answers）
- window.__streamConn hack 改為 Zustand store 管理
- clear() 同時重置 sessionId / model
- ConversationView handlers 用 useCallback memoize

### 改善

- TopBar 三模式按鈕（term / jsonl / stream）各自 active 樣式
- TopBar 底色提亮
- Settings 文字亮度對齊 SESSIONS 標題

## [0.1.1] - 2026-03-17

### 修復

- Terminal relay 生命週期：goroutine 互相取消，防止無限 block
- WebSocket write race condition：加入 mutex 保護
- Token 認證改用 constant-time 比較，防止 timing attack
- Token 認證支援 `?token=` query param（WebSocket 無法送 header）
- Session 建立失敗時 rollback tmux session（防止孤立 session）
- Delete handler 正確處理 ListSessions 錯誤
- Batcher 釋放 mutex 後再呼叫 onFlush（防止 deadlock）
- UpdateSession / UpdateGroup 正確回傳錯誤和 ErrNotFound
- Session name 驗證（`^[a-zA-Z0-9_-]+$`）

### 改善

- 自動掃描主機上既有的 tmux sessions（不需手動透過 API 建立）
- Terminal 全寬高顯示（修正 flex layout + 初始 resize 時序）
- WebSocket 連線後送出初始 resize（防止 tmux 按 80x24 渲染）
- URL encode session 名稱（支援空格、中文等特殊字元）
- Loading overlay 帶呼吸動畫，收到資料後 300ms fade out
- Session 按鈕加 cursor pointer + 切換後自動 focus terminal
- Sidebar 文字亮度提升
- Zustand persist 只存 activeId（避免快取過期 session 資料）
- WebSocket onmessage 加 ArrayBuffer type guard
- ResizeObserver 用 requestAnimationFrame debounce
- ws.ts 修正 onerror + onclose 重複觸發 onClose

## [0.1.0] - 2026-03-17

Phase 1: Daemon 基礎 + Terminal 模式

### 新增

- **tbox daemon** — Go HTTP + WebSocket API server
  - Config 載入（TOML，自動讀取 `~/.config/tbox/config.toml`）
  - SQLite 持久化（sessions / groups CRUD）
  - tmux session 管理（建立 / 刪除 / 列出）
  - Terminal relay（WebSocket ↔ PTY 雙向中繼，含 resize）
  - DataBatcher（16ms / 64KB 輸出批次化）
  - 安全：IP 白名單（CIDR）、token 認證（constant-time 比較）、CORS
  - Session name 驗證（`^[a-zA-Z0-9_-]+$`）
  - Graceful shutdown（SIGTERM / SIGINT）

- **tbox spa** — React SPA（獨立部署）
  - Session 面板（左側選單，模式圖示，active 高亮）
  - Terminal 畫面（xterm.js + WebGL + FitAddon + resize）
  - API client（可設定 daemon base URL）
  - Session store（Zustand + localStorage 持久化）

### 架構

- Daemon 和 SPA 完全分離部署
- Daemon 是純 API server，不含前端檔案
- SPA 可封裝為 Electron 或放在獨立主機上 serve

### 技術棧

- Daemon: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite / BurntSushi/toml
- SPA: React 19 / Vite / xterm.js / Zustand / Tailwind CSS / Vitest
