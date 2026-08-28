import { create } from "zustand";

export interface AppState {
  selectedDomain: any | null;
  selectedSubCategory: any | null;
  lastQuestion: string;
  isThinking: boolean;
}

interface AppStore extends AppState {
  setSelectedDomainKey: (key?: string) => void;
  setLastQuestion: (question: string) => void;
  clearSelection: () => void;
  newConversation: () => void;
  updateMessageHistory: (messages: any[]) => void;
  setThinking: (thinking: boolean) => void;

  // Internal helpers for domain/subcat selection
  _updateSelectedDomain: (key?: string, domainData?: any) => void;
  _updateSelectedSubCategory: (display?: string | null) => void;
}

const initialState: AppState = {
  selectedDomain: null,
  selectedSubCategory: null,
  lastQuestion: "",
  isThinking: false,
};

export const useAppStore = create<AppStore>((set) => ({
  ...initialState,

  setSelectedDomainKey: (key) =>
    set(() => ({ 
      selectedDomain: initialState.selectedDomain ? { ...initialState.selectedDomain, key } : null 
    })),

  setLastQuestion: (question) => {
    // Persist to secure storage via Go bridge
    if ((window as any).wails?.app.setLastQuestion) {
      try {
        (window as any).wails.app.setLastQuestion(question);
      } catch(e){}
    }
    set({ lastQuestion: question });
  },
  
  clearSelection: () => set({ selectedDomain: null, selectedSubCategory: null }),

  newConversation: () => set(() => ({ /* keep domain/subcat but fresh state */ })),

  updateMessageHistory: (_messages) => {
    // Messages are stored in components' local state
  },

  setThinking: (thinking) => set({ isThinking: thinking }),

  _updateSelectedDomain: (key?, domainData?) =>
    set(() => ({ 
      selectedDomain: key && domainData ? { ...domainData, key } : null 
    })),

  _updateSelectedSubCategory: (display?) =>
    set({ selectedSubCategory: display ? { display_name: display!, icon: "" } : null }),
}));
