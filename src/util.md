# 通用字段（VenueBase）

venue_id：字符串，主键

type："attraction" | "restaurant"

name：名称（多语言可附 name_en/name_local）

location：{country, city, district, lat, lon}

tags：["亲子","网红","本地特色","深夜"]

price_level：0-4（越高越贵）

hours：[{day:"Mon", open:"10:00", close:"18:00"}]

amenities：["wifi","parking","pet-friendly","wheelchair"]

suitable_for：["亲子","背包客","商务","情侣"]

avg_rating：0.0-5.0（可拆分为人均打分/维度打分）

data_quality：{has_photos:boolean, reviews_count:int, last_update:ISO8601}

# 景点特有（AttractionExt）

duration_recommend："1-2h"

crowd_level："低/中/高（含时段）"

best_season：["4-5月","10-11月"]

highlights：["日落观景台","古城墙"]

booking_needed：true/false

ticket_info：{adult: xxx, child: xxx, notes: "..."}

access：{transport: ["JR**站步行10分钟","巴士xx线"], barrier_free: boolean}

# 餐厅特有（RestaurantExt）

cuisines：["和食","烧鸟"]

signature_dishes：[{name:"盐烤鸡腿", price: 680, spicy:0-3}]

reservation：{required:boolean, phone:"", url:""}

wait_time："工作日中午≈15min"

menu_price_range：{min: , max: }

dietary：{vegan:boolean, vegetarian:boolean, halal:boolean, gluten_free:boolean}