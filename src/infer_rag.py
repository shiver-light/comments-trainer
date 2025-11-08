import json, faiss, numpy as np, os
from transformers import AutoTokenizer, AutoModelForCausalLM, pipeline
from peft import PeftModel
from pathlib import Path

BASE_MODEL = os.environ.get("BASE_MODEL", "Qwen/Qwen2.5-7B-Instruct")
LORA_DIR   = "out-lora"

# 1) 读取语料
corpus = [json.loads(l) for l in Path("data/corpus.jsonl").read_text(encoding="utf-8").splitlines()]
chunks = []
meta = []
for doc in corpus:
    for c in doc["chunks"]:
        chunks.append(c)
        meta.append({"venue_id":doc["venue_id"], "name":doc["name"], "type":doc["type"]})

# 2) 简易向量：用模型 tokenizer 的词频向量代替（演示用，实际请用 embedding 模型）
def embed(texts):
    # 极简 Bag-of-words hash 向量（占位）。生产请换 e5/jina 等专业 embedding。
    dim = 2048
    vs = np.zeros((len(texts), dim), dtype="float32")
    for i,t in enumerate(texts):
        for w in t.split():
            vs[i, hash(w)%dim] += 1.0
    # 归一
    norms = np.linalg.norm(vs, axis=1, keepdims=True)+1e-9
    return vs / norms

xb = embed(chunks)
index = faiss.IndexFlatIP(xb.shape[1]); index.add(xb)

def retrieve(query, topk=5):
    qv = embed([query])
    D,I = index.search(qv, topk)
    return [chunks[i] for i in I[0]]

# 3) 加载模型（合并 LoRA）
tok = AutoTokenizer.from_pretrained(BASE_MODEL)
base = AutoModelForCausalLM.from_pretrained(BASE_MODEL, device_map="auto")
model = PeftModel.from_pretrained(base, LORA_DIR).merge_and_unload()

pipe = pipeline("text-generation", model=model, tokenizer=tok, device_map="auto")

PROMPT_TMPL = """你是一名中立的旅行/美食点评助理。根据【检索要点】生成中文点评，结构：
1）一句话总结（≤25字）
2）3–5个要点（客观事实，可核验）
3）适合人群 & 避坑建议
不得凭空捏造未出现的事实。
【场景】{scene}
【检索要点】
{bullets}
"""

def generate_review(scene, user_query):
    bullets = retrieve(user_query, topk=5)
    prompt = PROMPT_TMPL.format(scene=scene, bullets="\n- " + "\n- ".join(bullets))
    out = pipe(prompt, max_new_tokens=320, do_sample=False)[0]["generated_text"]
    return out.split("【检索要点】")[-1] if "【检索要点】" in out else out

if __name__ == "__main__":
    print(generate_review("restaurant/大阪/炭火鸟治", "大阪 烧鸟 人均 2k 排队 油烟"))
