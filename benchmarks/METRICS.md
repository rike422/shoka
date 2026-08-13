# チェック基準（類似ツール・論文の参照）

公開数字は README の Benchmarks と `results/` を正とする。非公開コーパスの生データは `../local/`。

shoka のオフライン評価は、コード検索 / エージェント向け retrieval でよく使われる指標に揃える。

## 参照した基準

| 出典 | 何を見ているか | 採用する指標 |
| --- | --- | --- |
| [Zoekt e2e ranking](https://github.com/sourcegraph/zoekt/pull/714)（Sourcegraph） | コード検索の「欲しい 1 ファイルが上に来るか」 | **Recall@1 / Recall@5 / MRR** |
| [Agent Retrieval Bench](https://agent-retrieval-bench.github.io/) | エージェントが次に必要なファイル集合 | **Recall@k（gold 被覆）**, **MRR**, （参考）予算付き収率 |
| 一般的 IR / RAG offline eval | ランキング品質の定番 | Recall@k, MRR（必要なら nDCG） |
| [BM25 Wins at Scale](https://arxiv.org/abs/2607.26497) | コーパス規模を変えつつ同一質問で比較 | 規模の明示・コスト併記（今回は単一規模） |

Zoekt 側の議論要約: コード検索は「関連度がグラデーションの大量ドキュメント」より **正解が 1–2 ファイル**のことが多いので、nDCG より **Recall@k + MRR** の方が体験に近い。

Agent Retrieval Bench 要約: 単なる類似コードではなく **ワークフロー上必要なファイル**を gold にし、Recall@k / MRR に加え、トークン予算内に gold が何割入るか（BCY）も見る。

## shoka で測る指標（正式）

タスク JSON の各問は `query` と `want_paths`（1 個以上の正解パス）を持つ。

| 指標 | 定義 | 備考 |
| --- | --- | --- |
| **Success@k** | top-k に **いずれか**の want が入った問の割合 | 「一発で使えるか」。ブログの分かりやすい数字 |
| **Recall@k (coverage)** | top-k に入った want 数 / 全 want 数（問ごとに平均） | 複数 gold 向け。Agent Retrieval Bench 寄り |
| **Recall@1 / @5 / @10** | 上と同じく Success 版と coverage 版を併記 | Zoekt は Success に近い（単一 target） |
| **MRR** | 最初の want の順位 r について `1/r` の平均。無しは 0 | Zoekt / ARB 共通 |
| **Lines-to-first-gold** | 順位どおりに開いて最初の want までの行数 | エージェント読取の代理（B+） |
| **Latency** | クエリあたり壁時計 ms | **複数回**取り mean ± stdev |
| **Index time / DB size** | フル再建・増分 noop | **複数回**取り mean ± stdev |

ベースライン: 同一 `query` に対する **ripgrep `-l -F`**（gitignore 相当）。埋め込み検索は今回の比較対象外（「Grep の次の一手」が主題のため）。

## 複数回測定の方針

| 対象 | 回数の目安 | 理由 |
| --- | --- | --- |
| ランキング系（Success / Recall / MRR / 行数） | 1 回でよい（結果を記録） | 同一 index・同一クエリなら決定的 |
| 検索レイテンシ | **≥5** | OS キャッシュでブレる |
| フル index / 増分 | **≥3**（可能なら 5） | ディスク・CPU でブレる |

「複数回」は主に **時間系の信頼区間**用。ランキングがランごとに変わるならバグか非決定的設定を疑う。

## 今回のコーパス規模（名前なし）

索引ファイル約 **1,100** / チャンク約 **2,800** / DB 約 **28 MB**。  
Agent Retrieval Bench や BM25-at-scale の大規模段とは別物で、「手元の中規模アプリに BM25 前段を足したときの Grep との差」を見るためのベンチ。

## 再現

```bash
# ランキング + 対 rg + レイテンシ複数回
RUNS=5 ./scripts/bench-suite.sh /path/to/repo eval/tasks.sample-corpus.json \
  > eval/results/compare.public.json

# 索引だけ複数回
INDEX_RUNS=5 ./scripts/bench-index-multi.sh /path/to/repo
```
