import React, { useState } from "react";
import { DomainPill } from "./DomainPill";
import { SubCategoryPill } from "./SubCategoryPill";
import { useApi } from "../hooks/useApi";

export default function App() {
  const [selectedDomainKey, setSelectedDomainKey] = useState<string | null>(null);
  const [subCategories, setSubCategories] = useState<Record<string,{display_name:string;icon?:string}>>({});

  // API hooks - load domains on startup and expose sendMessage for chat
  const { domains, loadDomains } = useApi();

  React.useEffect(() => {
    if (domains.length === 0) {
      loadDomains();
    }
  }, [domains.length]);

  const handleDomainSelection = (domain: any) => {
    setSelectedDomainKey(domain.key);
    setSubCategories((domain as any).sub_categories || {});
  };

  return (
    <div className="app">
      {/* Domain pill */}
      {!selectedDomainKey ? (
        <DomainPill domains={domains} selected={null} onSelection={handleDomainSelection} />
      ) : null}

      {/* Sub category pills */}
      {selectedDomainKey && Object.keys(subCategories).length > 0 && (
        <SubCategoryPill
          domainName=""
          subCategories={subCategories}
          onSelection={() => setSelectedDomainKey(null)}
        />
      )}
    </div>
  );
}
