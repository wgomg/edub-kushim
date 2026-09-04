import * as pdfjsLib from 'pdfjs-dist';
import workerUrl from './pdf.worker.js?worker&url';

pdfjsLib.GlobalWorkerOptions.workerSrc = workerUrl;

export const getDocument = pdfjsLib.getDocument;
export const TextLayer = pdfjsLib.TextLayer;
