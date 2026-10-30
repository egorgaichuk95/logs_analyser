import { CaseMeta, AnalyzeCaseResponse } from '../api/client';

export type AppPageId =
  | 'overview'
  | 'ingest'
  | 'hypotheses'
  | 'topology'
  | 'chains'
  | 'evidence'
  | 'classic_v1';

export interface TopNavHeaderProps {
  activeCase: CaseMeta | null;
  activePage: AppPageId;
  analysis: AnalyzeCaseResponse | null;
  onSelectPage: (page: AppPageId) => void;
  onSelectCase: (caseMeta: CaseMeta) => void;
  onCaseCreated: (caseMeta: CaseMeta) => void;
  onCaseDeleted: (caseId: string) => void;
  onAnalyzeSuccess: (result: AnalyzeCaseResponse) => void;
  onPurgeSuccess: () => void;
}

export interface IngestAndTiltPageProps {
  activeCase: CaseMeta;
  onIngestComplete: () => void;
  onAnalyzeSuccess: (result: AnalyzeCaseResponse) => void;
  onNavigateToHypotheses: () => void;
}
