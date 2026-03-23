import json
from collections import defaultdict
from pathlib import Path

SRC = Path("crawler/data/reviews.jsonl")   # 爬虫输出（由 crawler/main.go 生成）
CORPUS_OUT = Path("data/corpus.jsonl")     # 给 infer_rag.py 用
SFT_OUT = Path("data/sft_reviews.jsonl")   # 给 train_lora.py 用


def load_reviews(path: Path):
    items = []
    with path.open("r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                items.append(json.loads(line))
            except Exception:
                continue
    return items


def build_corpus(reviews):
    """按餐厅聚合，生成 infer_rag 需要的语料结构。

    目标格式：
    {"venue_id": str, "name": str, "type": str, "chunks": [str, ...]}
    """
    groups = defaultdict(list)
    meta = {}
    for r in reviews:
        key = r.get("restaurant_url") or (r.get("platform"), r.get("restaurant"))
        groups[key].append(r)

    docs = []
    for i, (key, rs) in enumerate(groups.items()):
        # 简单 venue_id：用索引 + 平台
        platforms = {r.get("platform", "") for r in rs}
        platform = "+".join(sorted(p for p in platforms if p)) or "unknown"
        name = rs[0].get("restaurant") or "未知店铺"
        venue_id = f"{platform}_{i}"

        # chunks：把评论内容拼成若干段，简单切分
        texts = []
        for r in rs:
            content = (r.get("content") or "").strip()
            if not content:
                continue
            date = r.get("date") or ""
            user = r.get("user") or "匿名用户"
            txt = f"【{date} {user}】{content}"
            texts.append(txt)

        if not texts:
            continue

        # 这里直接把每条评论当一个 chunk
        doc = {
            "venue_id": venue_id,
            "name": name,
            "type": platform,
            "chunks": texts,
        }
        docs.append(doc)

    return docs


def build_sft_data(reviews):
    """构造简单的 SFT 样本：
    instruction: 让模型生成点评
    input: {platform, restaurant, samples: [...]}
    output: 合成的一条“总结点评”（这里仅占位，可后续人工/规则填充）

    这里先用几条评论拼成一个长文本作为 output，占位用，
    你后续可以改成手工标注或用规则生成更干净的目标文本。
    """
    groups = defaultdict(list)
    for r in reviews:
        key = r.get("restaurant_url") or (r.get("platform"), r.get("restaurant"))
        groups[key].append(r)

    data = []
    for (key, rs) in groups.items():
        if len(rs) < 3:
            continue  # 太少的点不做 SFT
        platform = rs[0].get("platform", "")
        name = rs[0].get("restaurant") or "未知店铺"

        samples = []
        for r in rs[:10]:  # 限制最多 10 条
            samples.append({
                "user": r.get("user"),
                "date": r.get("date"),
                "rating": r.get("rating"),
                "content": r.get("content"),
            })

        # output：先简单拼接几条评论，后续可以人工清洗/替换
        merged = "\n".join(
            f"[{s.get('date') or ''} {s.get('user') or ''}] {s.get('content') or ''}" for s in samples
        )

        ex = {
            "instruction": "根据给出的多条用户评价，生成一段结构化的中文点评总结。",
            "input": {
                "platform": platform,
                "restaurant": name,
                "samples": samples,
            },
            "output": merged,
        }
        data.append(ex)

    return data


def main():
    if not SRC.exists():
        raise SystemExit(f"源文件不存在: {SRC}，请先运行 crawler/main.go 生成 reviews.jsonl")

    reviews = load_reviews(SRC)
    print(f"loaded {len(reviews)} reviews")

    corpus_docs = build_corpus(reviews)
    CORPUS_OUT.parent.mkdir(parents=True, exist_ok=True)
    with CORPUS_OUT.open("w", encoding="utf-8") as f:
        for doc in corpus_docs:
            f.write(json.dumps(doc, ensure_ascii=False) + "\n")
    print(f"wrote {len(corpus_docs)} docs to {CORPUS_OUT}")

    sft_data = build_sft_data(reviews)
    SFT_OUT.parent.mkdir(parents=True, exist_ok=True)
    with SFT_OUT.open("w", encoding="utf-8") as f:
        for ex in sft_data:
            f.write(json.dumps(ex, ensure_ascii=False) + "\n")
    print(f"wrote {len(sft_data)} sft samples to {SFT_OUT}")


if __name__ == "__main__":
    main()
