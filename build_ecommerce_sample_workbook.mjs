import fs from "node:fs/promises";
import path from "node:path";
import { SpreadsheetFile, Workbook } from "@oai/artifact-tool";

const outputDir = path.resolve("outputs/20260730-ecommerce-simulation");
const outputPath = path.join(outputDir, "跨境电商订单营销模拟数据.xlsx");
const previewPath = path.join(outputDir, "订单营销明细预览.png");

function seededRandom(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 4294967296;
  };
}

const random = seededRandom(20260730);
const pick = (items) => items[Math.floor(random() * items.length)];
const round = (value) => Math.round(value * 100) / 100;

const products = [
  { sku: "SKN-001", name: "烟酰胺焕亮精华", category: "护肤美容", price: 29.9, cost: 9.2 },
  { sku: "SKN-002", name: "玻尿酸保湿面霜", category: "护肤美容", price: 24.9, cost: 7.6 },
  { sku: "HOM-001", name: "可折叠收纳箱", category: "家居收纳", price: 32.9, cost: 12.8 },
  { sku: "HOM-002", name: "抽屉分隔收纳套装", category: "家居收纳", price: 18.9, cost: 5.9 },
  { sku: "OUT-001", name: "轻量运动水壶", category: "运动户外", price: 21.9, cost: 6.7 },
  { sku: "OUT-002", name: "防水徒步腰包", category: "运动户外", price: 35.9, cost: 13.5 },
  { sku: "PET-001", name: "宠物慢食碗", category: "宠物用品", price: 16.9, cost: 4.6 },
  { sku: "PET-002", name: "可降解宠物拾便袋", category: "宠物用品", price: 14.9, cost: 3.8 },
];

const countries = [
  { name: "美国", shipping: 6.8 },
  { name: "加拿大", shipping: 8.2 },
  { name: "英国", shipping: 7.1 },
  { name: "澳大利亚", shipping: 9.4 },
];

const channels = [
  { name: "Google Ads", campaigns: ["Brand_Search_US", "Summer_Skincare", "Home_Storage_Search"], costRate: 0.16, clickRate: 1.2, impressionRate: 68 },
  { name: "Meta Ads", campaigns: ["Meta_Retargeting", "Beauty_Lookalike", "Pet_Video_Prospecting"], costRate: 0.19, clickRate: 1.4, impressionRate: 81 },
  { name: "TikTok Ads", campaigns: ["TikTok_Ugc_Beauty", "TikTok_Pet_Creators", "TikTok_Outdoor_Summer"], costRate: 0.22, clickRate: 1.7, impressionRate: 104 },
  { name: "自然搜索", campaigns: ["SEO_Organic"], costRate: 0, clickRate: 0, impressionRate: 0 },
  { name: "EDM 邮件", campaigns: ["VIP_Repeat_Purchase", "New_User_Welcome"], costRate: 0.035, clickRate: 0.25, impressionRate: 11 },
  { name: "直接访问", campaigns: ["Direct_Traffic"], costRate: 0, clickRate: 0, impressionRate: 0 },
];

const headers = [
  "订单号", "下单日期", "国家地区", "渠道", "广告活动", "客户编号", "客户类型",
  "商品SKU", "商品名称", "商品品类", "销售数量", "商品单价_USD", "商品销售额_USD",
  "优惠金额_USD", "实付金额_USD", "商品成本_USD", "广告花费_USD", "物流费用_USD",
  "订单状态", "是否转化", "会话数", "广告点击量", "广告展示量", "毛利_USD", "下单小时"
];

const customerOrders = new Map();
const rows = [];
const startDate = new Date(Date.UTC(2026, 3, 1));

for (let dayOffset = 0; dayOffset < 90; dayOffset += 1) {
  const date = new Date(startDate);
  date.setUTCDate(startDate.getUTCDate() + dayOffset);
  const weekday = date.getUTCDay();
  const orderCount = 7 + Math.floor(random() * 5) + ([0, 6].includes(weekday) ? 2 : 0);

  for (let sequence = 1; sequence <= orderCount; sequence += 1) {
    const customerId = `C${String(1 + Math.floor(random() * 360)).padStart(4, "0")}`;
    const previousOrders = customerOrders.get(customerId) || 0;
    customerOrders.set(customerId, previousOrders + 1);

    const product = pick(products);
    const country = pick(countries);
    const channel = pick(channels);
    const quantity = random() < 0.76 ? 1 : random() < 0.93 ? 2 : 3;
    const gross = round(product.price * quantity);
    const discount = round(random() < 0.58 ? gross * (0.04 + random() * 0.11) : 0);
    const statusRoll = random();
    const status = statusRoll < 0.035 ? "已取消" : statusRoll < 0.095 ? "已退款" : "已完成";
    const paid = status === "已取消" ? 0 : round(gross - discount);
    const cost = status === "已取消" ? 0 : round(product.cost * quantity);
    const adSpend = status === "已取消" ? 0 : round(gross * channel.costRate * (0.78 + random() * 0.44));
    const shipping = status === "已取消" ? 0 : round(country.shipping + Math.max(0, quantity - 1) * 1.35);
    const converted = status === "已完成" ? 1 : 0;
    const sessions = 1 + Math.floor(random() * 3);
    const clicks = channel.clickRate === 0 ? 0 : Math.max(1, Math.round(channel.clickRate * (0.7 + random() * 0.7)));
    const impressions = channel.impressionRate === 0 ? 0 : Math.max(clicks * 20, Math.round(channel.impressionRate * (0.75 + random() * 0.6)));
    const profit = status === "已完成" ? round(paid - cost - adSpend - shipping) : round(-adSpend - shipping);
    const orderNumber = `ORD-${date.toISOString().slice(0, 10).replaceAll("-", "")}-${String(sequence).padStart(3, "0")}`;

    rows.push([
      orderNumber, date, country.name, channel.name, pick(channel.campaigns), customerId,
      previousOrders > 0 ? "复购" : "新客", product.sku, product.name, product.category, quantity,
      product.price, gross, discount, paid, cost, adSpend, shipping, status, converted,
      sessions, clicks, impressions, profit, 8 + Math.floor(random() * 14),
    ]);
  }
}

const workbook = Workbook.create();
const dataSheet = workbook.worksheets.add("订单营销明细");
const guideSheet = workbook.worksheets.add("数据说明");
dataSheet.showGridLines = false;
guideSheet.showGridLines = false;

dataSheet.getRangeByIndexes(0, 0, 1, headers.length).values = [headers];
dataSheet.getRangeByIndexes(1, 0, rows.length, headers.length).values = rows;
const dataRange = dataSheet.getRangeByIndexes(0, 0, rows.length + 1, headers.length);
const dataTable = dataSheet.tables.add(`A1:Y${rows.length + 1}`, true, "订单营销明细表");
dataTable.style = "TableStyleMedium2";
dataTable.showFilterButton = true;
dataTable.showBandedColumns = false;
dataSheet.freezePanes.freezeRows(1);

dataSheet.getRange(`A1:Y1`).format = {
  fill: "#0F5B78",
  font: { bold: true, color: "#FFFFFF", name: "Microsoft YaHei" },
  horizontalAlignment: "center",
  verticalAlignment: "center",
  wrapText: true,
};
dataSheet.getRange(`A1:Y${rows.length + 1}`).format.font = { name: "Microsoft YaHei", size: 10 };
dataSheet.getRange(`A1:Y${rows.length + 1}`).format.verticalAlignment = "center";
dataSheet.getRange(`A1:Y${rows.length + 1}`).format.borders = { preset: "insideHorizontal", style: "thin", color: "#DCE6ED" };
dataSheet.getRange(`B2:B${rows.length + 1}`).format.numberFormat = "yyyy-mm-dd";
dataSheet.getRange(`K2:K${rows.length + 1}`).format.numberFormat = "0";
dataSheet.getRange(`L2:R${rows.length + 1}`).format.numberFormat = "$#,##0.00";
dataSheet.getRange(`T2:T${rows.length + 1}`).format.numberFormat = "0";
dataSheet.getRange(`U2:W${rows.length + 1}`).format.numberFormat = "#,##0";
dataSheet.getRange(`X2:X${rows.length + 1}`).format.numberFormat = "$#,##0.00;[Red]-$#,##0.00";
dataSheet.getRange(`Y2:Y${rows.length + 1}`).format.numberFormat = "0";
dataSheet.getRange(`A1:Y${rows.length + 1}`).format.autofitColumns();
const columnWidths = [20, 13, 12, 15, 25, 13, 11, 12, 22, 14, 10, 14, 15, 14, 14, 14, 14, 14, 11, 10, 10, 12, 14, 14, 10];
columnWidths.forEach((width, index) => {
  dataSheet.getRangeByIndexes(0, index, rows.length + 1, 1).format.columnWidth = width;
});
dataSheet.getRange("A1:Y1").format.rowHeight = 30;

dataSheet.getRange(`S2:S${rows.length + 1}`).conditionalFormats.add("containsText", {
  text: "已退款",
  format: { fill: "#FDE2E1", font: { color: "#B42318" } },
});
dataSheet.getRange(`S2:S${rows.length + 1}`).conditionalFormats.add("containsText", {
  text: "已取消",
  format: { fill: "#FFF0C2", font: { color: "#8A5A00" } },
});
dataSheet.getRange(`X2:X${rows.length + 1}`).conditionalFormats.add("cellIs", {
  operator: "lessThan",
  formula: 0,
  format: { fill: "#FDE2E1", font: { color: "#B42318" } },
});

guideSheet.getRange("A1:H1").merge();
guideSheet.getRange("A1").values = [["跨境电商订单营销模拟数据 - 使用说明"]];
guideSheet.getRange("A1:H1").format = {
  fill: "#0F5B78",
  font: { bold: true, color: "#FFFFFF", name: "Microsoft YaHei", size: 16 },
  horizontalAlignment: "left",
  verticalAlignment: "center",
};
guideSheet.getRange("A1:H1").format.rowHeight = 32;

guideSheet.getRange("A3:B8").values = [
  ["数据集名称", "跨境电商订单营销模拟数据"],
  ["数据期间", "2026-04-01 至 2026-06-29（90 天）"],
  ["明细行数", rows.length],
  ["数据粒度", "每行代表一笔订单的商品与营销归因明细"],
  ["币种", "美元（USD）"],
  ["数据性质", "模拟数据，仅用于产品演示、测试和面试展示"],
];
guideSheet.getRange("A3:A8").format = { fill: "#DCEEF6", font: { bold: true, name: "Microsoft YaHei" } };
guideSheet.getRange("A3:B8").format = { borders: { preset: "outside", style: "thin", color: "#B8CBD7" }, verticalAlignment: "center", wrapText: true };

guideSheet.getRange("A10:B10").merge();
guideSheet.getRange("A10").values = [["核心字段与口径"]];
guideSheet.getRange("A10:B10").format = { fill: "#1D7FA8", font: { bold: true, color: "#FFFFFF", name: "Microsoft YaHei" } };
guideSheet.getRange("A11:B16").values = [
  ["实付金额_USD", "商品销售额减去优惠金额；已取消订单为 0。"],
  ["毛利_USD", "已完成订单：实付金额 - 商品成本 - 广告花费 - 物流费用。"],
  ["是否转化", "已完成订单为 1；退款和取消订单为 0。"],
  ["客户类型", "同一客户首次订单为新客，后续订单为复购。"],
  ["ROI", "建议计算：已完成订单实付金额 / 广告花费。广告花费为 0 的自然流量、直接访问不计算 ROI。"],
  ["退款率", "建议计算：订单状态为已退款的订单数 / 总订单数。"],
];
guideSheet.getRange("A11:A16").format = { fill: "#EFF7FB", font: { bold: true, name: "Microsoft YaHei" } };
guideSheet.getRange("A11:B16").format = { borders: { preset: "insideHorizontal", style: "thin", color: "#DCE6ED" }, verticalAlignment: "top", wrapText: true };

guideSheet.getRange("D3:F3").merge();
guideSheet.getRange("D3").values = [["可直接向 Agent 提问"]];
guideSheet.getRange("D3:F3").format = { fill: "#F2A93B", font: { bold: true, color: "#1B2A34", name: "Microsoft YaHei" } };
guideSheet.getRange("D4:F10").merge(true);
guideSheet.getRange("D4:D10").values = [
  ["1. 最近 30 天销售额最高的 10 个商品是什么？"],
  ["2. 各渠道的广告花费、销售额和 ROI 分别是多少？"],
  ["3. 哪个国家的退款率最高？"],
  ["4. 复购客户的平均客单价和新客相比如何？"],
  ["5. 各商品品类的毛利和毛利率表现如何？"],
  ["6. 找出近 30 天销售额环比下降超过 20% 的商品。"],
  ["7. TikTok Ads 的哪些广告活动投入产出比较差？"],
];
guideSheet.getRange("D4:F10").format = { fill: "#FFF8EA", borders: { preset: "outside", style: "thin", color: "#E5C37D" }, font: { name: "Microsoft YaHei" }, verticalAlignment: "center", wrapText: true };
guideSheet.getRange("D4:F10").format.rowHeight = 28;

guideSheet.getRange("A18:F18").merge();
guideSheet.getRange("A18").values = [["导入提示：在产品中选择“导入 Excel”，上传此文件后确认“订单营销明细”作为数据集即可开始分析。当前 Agent 只读取第一个工作表，因此明细表已放在首个工作表。"]];
guideSheet.getRange("A18:F18").format = { fill: "#E9F6EF", font: { color: "#155D3A", name: "Microsoft YaHei" }, wrapText: true, verticalAlignment: "center" };
guideSheet.getRange("A18:F18").format.rowHeight = 42;
guideSheet.getRange("A1:H18").format.font = { name: "Microsoft YaHei", size: 10 };
guideSheet.getRange("A:A").format.columnWidth = 19;
guideSheet.getRange("B:B").format.columnWidth = 48;
guideSheet.getRange("C:C").format.columnWidth = 3;
guideSheet.getRange("D:F").format.columnWidth = 24;
guideSheet.freezePanes.freezeRows(2);

await fs.mkdir(outputDir, { recursive: true });
const preview = await workbook.render({ sheetName: "订单营销明细", range: "A1:Y18", scale: 1, format: "png" });
await fs.writeFile(previewPath, new Uint8Array(await preview.arrayBuffer()));
const output = await SpreadsheetFile.exportXlsx(workbook);
await output.save(outputPath);

const inspection = await workbook.inspect({
  kind: "workbook,sheet,table,region",
  sheetId: "订单营销明细",
  range: "A1:Y6",
  maxChars: 6000,
  tableMaxRows: 6,
  tableMaxCols: 25,
});
console.log(JSON.stringify({ outputPath, previewPath, rowCount: rows.length, inspection }, null, 2));
