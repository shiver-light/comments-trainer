from fastapi import FastAPI
from pydantic import BaseModel, Field
import uvicorn
from infer_rag import generate_review

app = FastAPI(title="Review Bot (Attraction & Restaurant)")

class ReviewReq(BaseModel):
    type: str = Field(pattern="^(attraction|restaurant)$")
    city: str
    name: str
    query: str  # 用于检索的关键词，如“日落 人流大 轮椅友好”或“烧鸟 排队 人均2000”

class ReviewResp(BaseModel):
    review: str

@app.post("/v1/review", response_model=ReviewResp)
def review(req: ReviewReq):
    scene = f"{req.type}/{req.city}/{req.name}"
    # 简单安全裁剪：限制长度，去除 URL/电话（演示）
    q = req.query[:200]
    for ban in ["http://","https://","+86"]:
        q = q.replace(ban, "")
    return ReviewResp(review=generate_review(scene, q))

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8080)
