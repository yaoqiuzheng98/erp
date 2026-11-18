// 历史预约来源回填（幂等，只处理 source 为空的记录）。
//
// 依据：
//   1. 后台建单会写 audit_logs(action=appt.create, target="姓名 日期 时段-结束")，
//      且审计时间与预约 created_at 基本同毫秒 → 匹配到即 admin。
//   2. 审计启用后仍无审计、且患者绑定了 open_id → 推断为微信小程序（wxapp）。
//   3. 其余（审计启用前 / 无 open_id / 匹配不上）保留空，表示历史未知，不猜。
//
// 用法：
//   docker exec -i erp-mongo mongosh --quiet < scripts/backfill-appt-source.js
//   ERP_DB=erp_test DRY_RUN=1 docker exec -i -e ERP_DB -e DRY_RUN erp-mongo mongosh --quiet < scripts/backfill-appt-source.js
const dbName = process.env.ERP_DB || 'erp';
const dryRun = process.env.DRY_RUN === '1';
const db = db.getSiblingDB(dbName);

const firstAudit = db.audit_logs.find({ action: 'appt.create' }).sort({ at: 1 }).limit(1).toArray()[0];
if (!firstAudit) {
  print('ERROR: 没有 appt.create 审计日志，无法回填');
  quit(1);
}

// 审计 target 是文本，按前缀匹配；名字里的正则字符全部转义。
function escapeRe(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function pickAdminAudit(a) {
  const prefix = a.patient_name + ' ' + a.date + ' ' + a.slot + '-';
  const re = new RegExp('^' + escapeRe(prefix));
  let best = null;
  let bestDiff = Infinity;
  db.audit_logs.find({ action: 'appt.create', target: re }).forEach((x) => {
    const diff = Math.abs(x.at.getTime() - a.created_at.getTime());
    if (diff < bestDiff) {
      bestDiff = diff;
      best = x;
    }
  });
  // 正常是建单后立刻写审计（毫秒级）；超过 1 分钟视为不同单，不硬配。
  return best && bestDiff <= 60 * 1000 ? best : null;
}

const counts = { admin: 0, wxapp: 0, unknown: 0 };
const unknownSamples = [];

db.appointments
  .find({ $or: [{ source: { $exists: false } }, { source: '' }, { source: null }] })
  .forEach((a) => {
    let src = '';
    if (pickAdminAudit(a)) {
      src = 'admin';
    } else if (a.created_at >= firstAudit.at) {
      const p = db.patients.findOne({ _id: a.patient_id }, { open_id: 1 });
      if (p && p.open_id) src = 'wxapp';
    }

    if (!src) {
      counts.unknown++;
      if (unknownSamples.length < 10) {
        unknownSamples.push(
          a.patient_name + ' ' + a.date + ' ' + a.slot + ' (created ' + a.created_at.toISOString() + ')'
        );
      }
      return;
    }

    counts[src]++;
    if (!dryRun) {
      db.appointments.updateOne({ _id: a._id }, { $set: { source: src } });
    }
  });

print('db=' + dbName + ' dryRun=' + dryRun);
print('backfilled admin=' + counts.admin + ' wxapp=' + counts.wxapp + ' unknown=' + counts.unknown);
unknownSamples.forEach((s) => print('unknown_sample=' + s));
