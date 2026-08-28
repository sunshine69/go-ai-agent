import React from "react";

interface SubCategory {
  key: string;
  display_name: string;
  icon?: string;
}

interface SubCategoryPillProps {
  domainName: string;
  subCategories: Record<string, {display_name: string; icon?:string}>;
  onSelection: (subCategory?: SubCategory) => void;
  selected?: SubCategory | null;
}

export const SubCategoryPill: React.FC<SubCategoryPillProps> = ({
  domainName,
  subCategories,
  onSelection,
  selected,
}) => {
  // Convert object to array
  const subCatArray = Object.entries(subCategories || {}).map(([key, value]) => ({ key, ...value }));

  return (
    <div className="sub-category-pill-container">
      <h2 className="sub-category-pill-title">{domainName}</h2>
      <div className="pill-list">
        {Object.keys(subCategories).length > 0 && (
          <button
            className=" pill back"
            onClick={() => onSelection()}
          >
            <span className="pill-text">← Back to domains</span>
          </button>
        )}
        {subCatArray.map((subCat: SubCategory) => (
          <button
            key={subCat.key}
            className={
              selected?.key === subCat.key ? "pill selected" : "pill"
            }
            onClick={() => onSelection(subCat)}
          >
            <span className="pill-icon" aria-hidden="true">{subCat.icon || ''}</span>
            <span className="pill-text">{subCat.display_name}</span>
          </button>
        ))}
      </div>
    </div>
  );
};
