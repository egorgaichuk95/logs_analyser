import type { Hypothesis } from '../api/client';
export type { TimelinePoint } from '../api/client';

export interface ServiceColorTheme {
  hex: string;
  bgSoft: string;
  border: string;
  text: string;
}

const PALETTE: ServiceColorTheme[] = [
  // 0: Кораллово-рубиновый
  { hex: '#f43f5e', bgSoft: 'rgba(244, 63, 94, 0.14)', border: 'rgba(244, 63, 94, 0.4)', text: '#fda4af' },
  // 1: Янтарно-оранжевый
  { hex: '#f59e0b', bgSoft: 'rgba(245, 158, 11, 0.14)', border: 'rgba(245, 158, 11, 0.4)', text: '#fcd34d' },
  // 2: Изумрудно-зелёный
  { hex: '#10b981', bgSoft: 'rgba(16, 185, 129, 0.14)', border: 'rgba(16, 185, 129, 0.4)', text: '#6ee7b7' },
  // 3: Аметистово-фиолетовый
  { hex: '#8b5cf6', bgSoft: 'rgba(139, 92, 246, 0.14)', border: 'rgba(139, 92, 246, 0.4)', text: '#c4b5fd' },
  // 4: Бирюзово-циановый
  { hex: '#06b6d4', bgSoft: 'rgba(6, 182, 212, 0.14)', border: 'rgba(6, 182, 212, 0.4)', text: '#67e8f9' },
  // 5: Пурпурно-розовый
  { hex: '#ec4899', bgSoft: 'rgba(236, 72, 153, 0.14)', border: 'rgba(236, 72, 153, 0.4)', text: '#f472b6' },
  // 6: Лаймовый
  { hex: '#84cc16', bgSoft: 'rgba(132, 204, 22, 0.14)', border: 'rgba(132, 204, 22, 0.4)', text: '#bef264' },
  // 7: Золотистый
  { hex: '#eab308', bgSoft: 'rgba(234, 179, 8, 0.14)', border: 'rgba(234, 179, 8, 0.4)', text: '#fde047' },
];

const KNOWN_SERVICE_COLOR_INDEX: Record<string, number> = {
  'chat-com': 0, // ruby
  'chat-session': 1, // amber
  'evidence-service': 2, // emerald
  'crm-service': 3, // violet
  'auth-service': 4, // cyan
  'billing-service': 5, // pink
  'tilt-collector': 6, // lime
  'api-gateway': 7, // gold
};

export function getRoleRussianLabel(role: Hypothesis['role']): string {
  switch (role) {
    case 'ROOT_CAUSE':
      return '🔴 Первопричина аварии (источник сбоя)';
    case 'PROPAGATION':
      return '🟠 Следствие (цепная реакция от первопричины)';
    case 'SYMPTOM':
      return '🟡 Внешний симптом (ошибки у клиентов на входе)';
    case 'NOISE':
      return '🟢 Фоновый шум (было и до аварии, не влияет на сбой)';
    default:
      return '❓ Неизвестная роль';
  }
}

export function getRoleShortRussianLabel(role: Hypothesis['role']): string {
  switch (role) {
    case 'ROOT_CAUSE':
      return 'Первопричина';
    case 'PROPAGATION':
      return 'Следствие по цепочке';
    case 'SYMPTOM':
      return 'Внешний симптом';
    case 'NOISE':
      return 'Фоновый шум';
    default:
      return 'Неизвестно';
  }
}

export function getErrorClassRussianLabel(errorClass: string): string {
  const norm = (errorClass || '').trim().toUpperCase();
  switch (norm) {
    case 'OWN_DEFECT':
      return 'Внутренняя ошибка в коде сервиса (паника / сбой логики)';
    case 'RESOURCE':
      return 'Исчерпание ресурсов (нехватка соединений к БД / памяти / пула)';
    case 'DEADLINE':
      return 'Превышено время ожидания ответа от другого сервиса (таймаут)';
    case 'PEER_5XX':
      return 'Соседний сервис вернул ошибку 5xx при вызове';
    case 'CANCELED':
      return 'Клиент сам оборвал запрос, не дождавшись ответа';
    case 'CLIENT_4XX':
      return 'Ошибка параметров клиентского запроса (4xx)';
    case 'AUTH':
      return 'Ошибка доступа или авторизации';
    default:
      return 'Общая ошибка выполнения';
  }
}

export function getSignalRussianInfo(signalKey: string): { title: string; description: string } {
  const norm = (signalKey || '').trim().toLowerCase();
  switch (norm) {
    case 's1':
    case 'trace_chain_score':
    case 'trace_chain':
      return {
        title: 'S1. Трассировка сквозной цепочки (Trace Chain)',
        description: 'Определяет участие сервиса в цепочке распределенной трассировки инцидента.',
      };
    case 's2':
    case 'onset_score':
    case 'onset':
    case 'onset_priority':
      return {
        title: 'S2. Опережение времени старта (Onset Priority)',
        description: 'Временной приоритет первого появления аномалии относительно других сервисов.',
      };
    case 's3':
    case 'lead_lag_score':
    case 'lead_lag':
      return {
        title: 'S3. Временное опережение кросс-корреляции (Lead-Lag)',
        description: 'Временной сдвиг (смещение пиков кросс-корреляции Пирсона с опережением других сервисов).',
      };
    case 's4':
    case 'novelty_score':
    case 'novelty':
      return {
        title: 'S4. Новизна шаблона ошибки (Novelty Score)',
        description: 'Степень новизны шаблона ошибки (отсутствие или единичные случаи в базовом периоде).',
      };
    case 's5':
    case 'causal_dir_score':
    case 'causal_dir':
    case 'causal_direction':
      return {
        title: 'S5. Причинное направление топологии (Causal Direction)',
        description: 'Направление графа вызовов: возникновение ошибки в конечном узле-исполнителе.',
      };
    case 's6':
    case 'graph_root_score':
    case 'graph_root':
      return {
        title: 'S6. Корень ориентированного графа (Graph Root)',
        description: 'Близость сервиса к источнику сбоя в направленном ациклическом графе связей.',
      };
    case 's7':
    case 'localization_score':
    case 'localization':
      return {
        title: 'S7. Локализация по поду / маршруту (Localization)',
        description: 'Концентрация ошибок на конкретном поде (перекос реплик) или маршруте API.',
      };
    case 's8':
    case 'change_corr_score':
    case 'change_corr':
    case 'change_correlation':
      return {
        title: 'S8. Корреляция с изменениями (Change Correlation)',
        description: 'Совпадение по времени с выкаткой новой версии или перезапуском пода.',
      };
    default:
      return {
        title: `Метрика ${signalKey}`,
        description: 'Статистическая оценка влияния данного сигнала в ранжировании первопричины.',
      };
  }
}

export function calculateIncidentDurationMinutes(startIso?: string, endIso?: string): number {
  if (!startIso || !endIso) return 5;
  const start = new Date(startIso).getTime();
  const end = new Date(endIso).getTime();
  if (Number.isNaN(start) || Number.isNaN(end) || end <= start) {
    return 5;
  }
  const minutes = Math.round((end - start) / 60000);
  return Math.max(1, minutes);
}

export function estimateHypothesisProblemLogsCount(
  h: Hypothesis,
  incidentDurationMinutes: number
): number {
  const duration = incidentDurationMinutes > 0 ? incidentDurationMinutes : 1;
  const rate = Math.max(0, h.incident_rate_per_min || 0);
  if (rate <= 0) return 0;
  return Math.max(1, Math.round(rate * duration));
}

export function computeLagSecondsFromRoot(
  h: Hypothesis,
  rootCause: Hypothesis | null
): number | null {
  if (!rootCause) return null;
  if (h.id === rootCause.id) return 0;

  // 1. Проверяем lead_lag_peers у первопричины
  if (rootCause.lead_lag_peers && rootCause.lead_lag_peers.length > 0) {
    const peerLink = rootCause.lead_lag_peers.find((p) => p.peer_service === h.service);
    if (peerLink && typeof peerLink.lag_seconds === 'number') {
      const lag = Math.abs(peerLink.lag_seconds);
      if (lag > 0) return lag;
    }
  }

  // 2. Проверяем lead_lag_peers у текущей гипотезы
  if (h.lead_lag_peers && h.lead_lag_peers.length > 0) {
    const peerLink = h.lead_lag_peers.find((p) => p.peer_service === rootCause.service);
    if (peerLink && typeof peerLink.lag_seconds === 'number') {
      const lag = Math.abs(peerLink.lag_seconds);
      if (lag > 0) return lag;
    }
  }

  // 3. Вычисляем разницу по onset_time
  if (h.onset_time && rootCause.onset_time) {
    const tH = new Date(h.onset_time).getTime();
    const tRoot = new Date(rootCause.onset_time).getTime();
    if (!Number.isNaN(tH) && !Number.isNaN(tRoot)) {
      const diffSec = Math.round((tH - tRoot) / 1000);
      if (diffSec >= 0) return diffSec;
    }
  }

  // Для роли PROPAGATION возвращаем дефолтный лаг 2 сек, если временной ряд показал следствие
  if (h.role === 'PROPAGATION') {
    return 2;
  }

  return null;
}

export function getBestCorrelationWithRoot(
  h: Hypothesis,
  rootCause: Hypothesis | null
): number | null {
  if (!rootCause) return null;
  if (h.id === rootCause.id) return 1.0;

  let bestCorr: number | null = null;

  if (rootCause.lead_lag_peers) {
    for (const peer of rootCause.lead_lag_peers) {
      if (peer.peer_service === h.service && typeof peer.correlation_r === 'number') {
        const absR = Math.abs(peer.correlation_r);
        if (bestCorr === null || absR > bestCorr) {
          bestCorr = absR;
        }
      }
    }
  }

  if (h.lead_lag_peers) {
    for (const peer of h.lead_lag_peers) {
      if (peer.peer_service === rootCause.service && typeof peer.correlation_r === 'number') {
        const absR = Math.abs(peer.correlation_r);
        if (bestCorr === null || absR > bestCorr) {
          bestCorr = absR;
        }
      }
    }
  }

  if (bestCorr !== null) {
    return Math.min(1.0, Math.max(0.0, bestCorr));
  }

  if (h.signals && typeof h.signals.lead_lag_score === 'number' && h.signals.lead_lag_score > 0) {
    return Math.min(1.0, Math.max(0.0, h.signals.lead_lag_score));
  }

  if (h.role === 'PROPAGATION') {
    return 0.94;
  }

  return null;
}

export function getServiceColor(serviceName: string): ServiceColorTheme {
  const norm = (serviceName || '').trim().toLowerCase();
  if (Object.prototype.hasOwnProperty.call(KNOWN_SERVICE_COLOR_INDEX, norm)) {
    return PALETTE[KNOWN_SERVICE_COLOR_INDEX[norm]];
  }

  // Детерминированный хеш строки для любого другого названия сервиса
  let hash = 0;
  for (let i = 0; i < norm.length; i++) {
    hash = (hash << 5) - hash + norm.charCodeAt(i);
    hash |= 0;
  }
  const idx = Math.abs(hash) % PALETTE.length;
  return PALETTE[idx];
}

export function buildHumanRussianExplanation(
  h: Hypothesis,
  rootCause: Hypothesis | null,
  incidentDurationMinutes: number
): string {
  let timeStr = 'момент аварии';
  if (h.onset_time) {
    try {
      const d = new Date(h.onset_time);
      if (!Number.isNaN(d.getTime())) {
        timeStr = d.toLocaleTimeString('ru-RU', {
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
        });
      }
    } catch {
      timeStr = h.onset_time;
    }
  }

  const sourcePlace = h.source_place?.trim() || 'исходный код';
  const pod = h.dominant_pod?.trim() || 'все реплики';
  const humanErrorClass = getErrorClassRussianLabel(h.error_class);
  const baseline = Math.round(h.baseline_rate_per_min || 0);
  const incident = Math.round(h.incident_rate_per_min || 0);
  const totalErrors = estimateHypothesisProblemLogsCount(h, incidentDurationMinutes);

  switch (h.role) {
    case 'ROOT_CAUSE':
      return `Это главный источник аварии — чинить нужно именно здесь. Сбой начался в сервисе «${h.service}» (файл ${sourcePlace}, контейнер ${pod}) в ${timeStr} — раньше всех остальных проблем. Что произошло: ${humanErrorClass}. До аварии таких ошибок было ${baseline}/мин, а во время сбоя стало ${incident}/мин (оценочно ~${totalErrors} проблемных логов за время сбоя). Из-за отказа этого сервиса по цепочке посыпались ошибки в соседних сервисах.`;

    case 'PROPAGATION': {
      const rootService = rootCause?.service || 'первопричинном сервисе';
      const lag = computeLagSecondsFromRoot(h, rootCause) ?? 2;
      return `Это НЕ причина аварии, а её прямое последствие (цепная реакция). Сервис «${h.service}» (${sourcePlace}) начал выдавать ошибки через +${lag} сек после поломки первопричины («${rootService}»), так как не смог получить от неё ответ (${humanErrorClass}). Когда вы почините «${rootService}», эта ошибка уйдёт сама.`;
    }

    case 'SYMPTOM':
      return `Это внешний симптом, который заметили пользователи на входе в систему («${h.service}»). Возник из-за того, что внутренние сервисы перестали вовремя отвечать на запросы (${humanErrorClass}).`;

    case 'NOISE':
      return `Это постоянный фоновый шум в сервисе «${h.service}». Такие записи шли с одинаковой частотой (${incident}/мин) и до аварии, и во время неё. К текущему сбою они не имеют отношения.`;

    default:
      return `Сервис «${h.service}»: ${humanErrorClass}. Частота во время сбоя: ${incident}/мин (оценочно ~${totalErrors} проблемных логов).`;
  }
}
