import json, os
from datasets import load_dataset
from transformers import AutoModelForCausalLM, AutoTokenizer, TrainingArguments
from peft import LoraConfig, get_peft_model
from trl import SFTTrainer

BASE_MODEL = os.environ.get("BASE_MODEL", "Qwen/Qwen2.5-7B-Instruct")  # 可换 Mistral 等
DATA_PATH = "data/sft_reviews.jsonl"
OUTPUT_DIR = "out-lora"

def format_sample(ex):
    # 把 instruction/input/output 拼成单轮指令微调格式
    inp = json.dumps(ex["input"], ensure_ascii=False)
    prompt = f"指令：{ex['instruction']}\n输入：{inp}\n请生成："
    return {"text": prompt + ex["output"]}

ds = load_dataset("json", data_files=DATA_PATH, split="train")
ds = ds.map(format_sample, remove_columns=ds.column_names)

tok = AutoTokenizer.from_pretrained(BASE_MODEL)
model = AutoModelForCausalLM.from_pretrained(BASE_MODEL, device_map="auto")

peft_cfg = LoraConfig(
    r=16, lora_alpha=32, lora_dropout=0.05,
    target_modules=["q_proj","k_proj","v_proj","o_proj","gate_proj","up_proj","down_proj"]
)
model = get_peft_model(model, peft_cfg)

args = TrainingArguments(
    output_dir=OUTPUT_DIR, per_device_train_batch_size=2,
    gradient_accumulation_steps=8, learning_rate=2e-4,
    num_train_epochs=2, fp16=True, logging_steps=10, save_strategy="epoch"
)

trainer = SFTTrainer(
    model=model, tokenizer=tok, train_dataset=ds,
    dataset_text_field="text", max_seq_length=2048, args=args
)
trainer.train()
trainer.save_model()
tok.save_pretrained(OUTPUT_DIR)
print("LoRA trained & saved to", OUTPUT_DIR)
